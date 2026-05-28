package observability

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	DefaultMaxLogBytes = 25 * 1024 * 1024
	DefaultLimit       = 100
	MaxLimit           = 1000
)

var ErrUnavailable = errors.New("observability log sink unavailable")

type LogEntry struct {
	Time       time.Time      `json:"time"`
	Level      string         `json:"level"`
	Source     string         `json:"source"`
	ClientID   string         `json:"client_id,omitempty"`
	LocationID string         `json:"location_id,omitempty"`
	Message    string         `json:"message"`
	Attrs      map[string]any `json:"attrs,omitempty"`
}

type LogQuery struct {
	Level      string
	Source     string
	ClientID   string
	LocationID string
	Since      time.Time
	Until      time.Time
	Query      string
	Limit      int
}

type FileSink struct {
	path     string
	maxBytes int64
	mu       sync.Mutex
}

func NewFileSink(path string) *FileSink {
	return &FileSink{path: path, maxBytes: DefaultMaxLogBytes}
}

func NewFileSinkWithMaxBytes(path string, maxBytes int64) *FileSink {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxLogBytes
	}
	return &FileSink{path: path, maxBytes: maxBytes}
}

func (sink *FileSink) Append(ctx context.Context, entry LogEntry) error {
	if sink == nil || strings.TrimSpace(sink.path) == "" {
		return ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if entry.Time.IsZero() {
		entry.Time = time.Now().UTC()
	}
	entry.Time = entry.Time.UTC()
	entry.Level = strings.TrimSpace(entry.Level)
	if entry.Level == "" {
		entry.Level = "info"
	}
	entry.Source = strings.TrimSpace(entry.Source)
	if entry.Source == "" {
		entry.Source = "panel"
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("encode log entry: %w", err)
	}
	data = append(data, '\n')

	sink.mu.Lock()
	defer sink.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(sink.path), 0o755); err != nil {
		return fmt.Errorf("%w: create log directory: %v", ErrUnavailable, err)
	}
	if err := sink.rotateIfNeeded(int64(len(data))); err != nil {
		return err
	}
	file, err := os.OpenFile(sink.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("%w: open log file: %v", ErrUnavailable, err)
	}
	defer file.Close()
	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("%w: write log file: %v", ErrUnavailable, err)
	}
	return nil
}

func (sink *FileSink) Query(ctx context.Context, query LogQuery) ([]LogEntry, error) {
	if sink == nil || strings.TrimSpace(sink.path) == "" {
		return nil, ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	limit := normalizeLimit(query.Limit)
	file, err := os.Open(sink.path)
	if errors.Is(err, os.ErrNotExist) {
		return []LogEntry{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%w: open log file: %v", ErrUnavailable, err)
	}
	defer file.Close()

	var result []LogEntry
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var entry LogEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue
		}
		if !matches(entry, query) {
			continue
		}
		result = append(result, entry)
		if len(result) > limit {
			copy(result, result[len(result)-limit:])
			result = result[:limit]
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("%w: read log file: %v", ErrUnavailable, err)
	}
	return result, nil
}

func (sink *FileSink) rotateIfNeeded(incoming int64) error {
	info, err := os.Stat(sink.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%w: stat log file: %v", ErrUnavailable, err)
	}
	if info.Size()+incoming < sink.maxBytes {
		return nil
	}
	rotated := sink.path + ".1"
	_ = os.Remove(rotated)
	if err := os.Rename(sink.path, rotated); err != nil {
		return fmt.Errorf("%w: rotate log file: %v", ErrUnavailable, err)
	}
	return nil
}

func FormatText(entries []LogEntry) string {
	var builder strings.Builder
	for _, entry := range entries {
		builder.WriteString(entry.Time.UTC().Format(time.RFC3339))
		builder.WriteByte(' ')
		builder.WriteString(entry.Level)
		builder.WriteByte(' ')
		builder.WriteString(entry.Source)
		if entry.ClientID != "" {
			builder.WriteString(" client=")
			builder.WriteString(entry.ClientID)
		}
		if entry.LocationID != "" {
			builder.WriteString(" location=")
			builder.WriteString(entry.LocationID)
		}
		builder.WriteByte(' ')
		builder.WriteString(entry.Message)
		builder.WriteByte('\n')
	}
	return builder.String()
}

func normalizeLimit(limit int) int {
	if limit <= 0 {
		return DefaultLimit
	}
	if limit > MaxLimit {
		return MaxLimit
	}
	return limit
}

func matches(entry LogEntry, query LogQuery) bool {
	if query.Level != "" && !strings.EqualFold(entry.Level, query.Level) {
		return false
	}
	if query.Source != "" && entry.Source != query.Source {
		return false
	}
	if query.ClientID != "" && entry.ClientID != query.ClientID {
		return false
	}
	if query.LocationID != "" && entry.LocationID != query.LocationID {
		return false
	}
	if !query.Since.IsZero() && entry.Time.Before(query.Since) {
		return false
	}
	if !query.Until.IsZero() && entry.Time.After(query.Until) {
		return false
	}
	if query.Query != "" && !strings.Contains(strings.ToLower(entry.Message), strings.ToLower(query.Query)) {
		return false
	}
	return true
}

type SlogHandler struct {
	sink interface {
		Append(context.Context, LogEntry) error
	}
	source string
}

func NewSlogHandler(sink interface {
	Append(context.Context, LogEntry) error
}, source string) slog.Handler {
	return SlogHandler{sink: sink, source: source}
}

func (handler SlogHandler) Enabled(context.Context, slog.Level) bool {
	return handler.sink != nil
}

func (handler SlogHandler) Handle(ctx context.Context, record slog.Record) error {
	attrs := make(map[string]any)
	record.Attrs(func(attr slog.Attr) bool {
		attrs[attr.Key] = attr.Value.Any()
		return true
	})
	return handler.sink.Append(ctx, LogEntry{
		Time:    record.Time,
		Level:   slogLevel(record.Level),
		Source:  handler.source,
		Message: record.Message,
		Attrs:   attrs,
	})
}

func (handler SlogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return handler
}

func (handler SlogHandler) WithGroup(string) slog.Handler {
	return handler
}

type FanoutHandler struct {
	handlers []slog.Handler
}

func NewFanoutHandler(handlers ...slog.Handler) slog.Handler {
	return FanoutHandler{handlers: handlers}
}

func (handler FanoutHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, child := range handler.handlers {
		if child.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (handler FanoutHandler) Handle(ctx context.Context, record slog.Record) error {
	var result error
	for _, child := range handler.handlers {
		if child.Enabled(ctx, record.Level) {
			if err := child.Handle(ctx, record); err != nil {
				result = errors.Join(result, err)
			}
		}
	}
	return result
}

func (handler FanoutHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	children := make([]slog.Handler, 0, len(handler.handlers))
	for _, child := range handler.handlers {
		children = append(children, child.WithAttrs(attrs))
	}
	return FanoutHandler{handlers: children}
}

func (handler FanoutHandler) WithGroup(name string) slog.Handler {
	children := make([]slog.Handler, 0, len(handler.handlers))
	for _, child := range handler.handlers {
		children = append(children, child.WithGroup(name))
	}
	return FanoutHandler{handlers: children}
}

func slogLevel(level slog.Level) string {
	switch {
	case level >= slog.LevelError:
		return "error"
	case level >= slog.LevelWarn:
		return "warn"
	case level <= slog.LevelDebug:
		return "debug"
	default:
		return "info"
	}
}

package traffic

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"olcpanel/internal/netstack"
)

const localNodeID = "local"

type Counter struct {
	RXBytes int64
	TXBytes int64
}

type CounterReader interface {
	ReadCounter(context.Context, string) (Counter, error)
}

type ReloadFunc func(context.Context) error

type Options struct {
	Now    func() time.Time
	Reload ReloadFunc
	Logger *slog.Logger
}

type Sampler struct {
	db       *sql.DB
	reader   CounterReader
	now      func() time.Time
	reload   ReloadFunc
	logger   *slog.Logger
	mu       sync.Mutex
	observed map[string]clientRuntimeState
}

type clientRuntimeState struct {
	quotaExceeded bool
	expired       bool
}

type sampleLocation struct {
	ID       string
	ClientID string
}

type counterState struct {
	RXBytes       int64
	TXBytes       int64
	LastSampledAt time.Time
	ResetCount    int64
}

type clientRow struct {
	ID             string
	QuotaBytes     sql.NullInt64
	QuotaUsedBytes int64
	ExpiresAt      sql.NullString
}

func NewSampler(db *sql.DB, reader CounterReader, options Options) *Sampler {
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	return &Sampler{
		db:       db,
		reader:   reader,
		now:      options.Now,
		reload:   options.Reload,
		logger:   options.Logger,
		observed: make(map[string]clientRuntimeState),
	}
}

func (sampler *Sampler) Sample(ctx context.Context) error {
	if sampler == nil || sampler.db == nil {
		return errors.New("traffic sampler database is required")
	}
	if sampler.reader == nil {
		return errors.New("traffic counter reader is required")
	}
	now := sampler.now().UTC()
	locations, err := sampler.loadLocations(ctx)
	if err != nil {
		return err
	}

	for _, location := range locations {
		counter, err := sampler.reader.ReadCounter(ctx, location.ID)
		if err != nil {
			if isMissingCounter(err) {
				continue
			}
			return fmt.Errorf("read traffic counter for location %s: %w", location.ID, err)
		}
		if err := sampler.persistCounter(ctx, location, counter, now); err != nil {
			return err
		}
	}

	return sampler.reloadIfRuntimeStateCrossed(ctx, now)
}

func (sampler *Sampler) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		if err := sampler.Sample(ctx); err != nil {
			sampler.logger.Error("traffic sample failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (sampler *Sampler) loadLocations(ctx context.Context) ([]sampleLocation, error) {
	rows, err := sampler.db.QueryContext(ctx, `
SELECT l.id, l.client_id
FROM locations l
JOIN clients c ON c.id = l.client_id AND c.node_id = l.node_id
WHERE l.node_id = ? AND l.enabled = 1 AND c.enabled = 1
ORDER BY l.created_at, l.id`, localNodeID)
	if err != nil {
		return nil, fmt.Errorf("load traffic sample locations: %w", err)
	}
	defer rows.Close()

	var locations []sampleLocation
	for rows.Next() {
		var location sampleLocation
		if err := rows.Scan(&location.ID, &location.ClientID); err != nil {
			return nil, fmt.Errorf("scan traffic sample location: %w", err)
		}
		locations = append(locations, location)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate traffic sample locations: %w", err)
	}
	return locations, nil
}

func (sampler *Sampler) persistCounter(ctx context.Context, location sampleLocation, counter Counter, now time.Time) error {
	if counter.RXBytes < 0 || counter.TXBytes < 0 {
		return fmt.Errorf("traffic counter for location %s is negative", location.ID)
	}
	tx, err := sampler.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin traffic sample transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	state, found, err := loadCounterState(ctx, tx, location.ID)
	if err != nil {
		return err
	}
	if !found {
		if err := upsertCounterState(ctx, tx, location.ID, counter, now, 0); err != nil {
			return err
		}
		return tx.Commit()
	}

	reset := counter.RXBytes < state.RXBytes || counter.TXBytes < state.TXBytes
	if reset {
		if err := upsertCounterState(ctx, tx, location.ID, counter, now, state.ResetCount+1); err != nil {
			return err
		}
		return tx.Commit()
	}

	rxDelta := counter.RXBytes - state.RXBytes
	txDelta := counter.TXBytes - state.TXBytes
	if rxDelta > 0 || txDelta > 0 {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO traffic_counters(node_id, client_id, location_id, rx_bytes, tx_bytes, period_start, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
			localNodeID, location.ClientID, location.ID, rxDelta, txDelta, formatTime(state.LastSampledAt), formatTime(now)); err != nil {
			return fmt.Errorf("insert traffic counter delta: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
UPDATE clients
SET quota_used_bytes = quota_used_bytes + ?, updated_at = CURRENT_TIMESTAMP
WHERE node_id = ? AND id = ?`, rxDelta+txDelta, localNodeID, location.ClientID); err != nil {
			return fmt.Errorf("update client traffic usage: %w", err)
		}
	}
	if err := upsertCounterState(ctx, tx, location.ID, counter, now, state.ResetCount); err != nil {
		return err
	}
	return tx.Commit()
}

func loadCounterState(ctx context.Context, tx *sql.Tx, locationID string) (counterState, bool, error) {
	var state counterState
	var sampledAt string
	err := tx.QueryRowContext(ctx, `
SELECT rx_bytes, tx_bytes, last_sampled_at, reset_count
FROM traffic_counter_state
WHERE node_id = ? AND location_id = ?`, localNodeID, locationID).Scan(&state.RXBytes, &state.TXBytes, &sampledAt, &state.ResetCount)
	if errors.Is(err, sql.ErrNoRows) {
		return counterState{}, false, nil
	}
	if err != nil {
		return counterState{}, false, fmt.Errorf("read traffic counter state: %w", err)
	}
	parsed, err := parseTime(sampledAt)
	if err != nil {
		return counterState{}, false, fmt.Errorf("parse traffic counter sample time: %w", err)
	}
	state.LastSampledAt = parsed
	return state, true, nil
}

func upsertCounterState(ctx context.Context, tx *sql.Tx, locationID string, counter Counter, sampledAt time.Time, resetCount int64) error {
	if _, err := tx.ExecContext(ctx, `
INSERT INTO traffic_counter_state(node_id, location_id, rx_bytes, tx_bytes, last_sampled_at, reset_count, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(node_id, location_id) DO UPDATE SET
	rx_bytes = excluded.rx_bytes,
	tx_bytes = excluded.tx_bytes,
	last_sampled_at = excluded.last_sampled_at,
	reset_count = excluded.reset_count,
	updated_at = excluded.updated_at`,
		localNodeID, locationID, counter.RXBytes, counter.TXBytes, formatTime(sampledAt), resetCount, formatTime(sampledAt)); err != nil {
		return fmt.Errorf("write traffic counter state: %w", err)
	}
	return nil
}

func (sampler *Sampler) reloadIfRuntimeStateCrossed(ctx context.Context, now time.Time) error {
	states, err := sampler.loadClientRuntimeStates(ctx, now)
	if err != nil {
		return err
	}

	sampler.mu.Lock()
	shouldReload := false
	for id, state := range states {
		previous, seen := sampler.observed[id]
		if seen && ((!previous.quotaExceeded && state.quotaExceeded) || (!previous.expired && state.expired)) {
			shouldReload = true
		}
	}
	sampler.observed = states
	sampler.mu.Unlock()

	if shouldReload && sampler.reload != nil {
		if err := sampler.reload(ctx); err != nil {
			return fmt.Errorf("reload after traffic state transition: %w", err)
		}
	}
	return nil
}

func (sampler *Sampler) loadClientRuntimeStates(ctx context.Context, now time.Time) (map[string]clientRuntimeState, error) {
	rows, err := sampler.db.QueryContext(ctx, `
SELECT id, quota_bytes, quota_used_bytes, expires_at
FROM clients
WHERE node_id = ? AND enabled = 1
ORDER BY id`, localNodeID)
	if err != nil {
		return nil, fmt.Errorf("load client runtime states: %w", err)
	}
	defer rows.Close()

	states := make(map[string]clientRuntimeState)
	for rows.Next() {
		var row clientRow
		if err := rows.Scan(&row.ID, &row.QuotaBytes, &row.QuotaUsedBytes, &row.ExpiresAt); err != nil {
			return nil, fmt.Errorf("scan client runtime state: %w", err)
		}
		states[row.ID] = deriveRuntimeState(row, now)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate client runtime states: %w", err)
	}
	return states, nil
}

func deriveRuntimeState(row clientRow, now time.Time) clientRuntimeState {
	var state clientRuntimeState
	if row.QuotaBytes.Valid && row.QuotaUsedBytes >= row.QuotaBytes.Int64 {
		state.quotaExceeded = true
	}
	if row.ExpiresAt.Valid {
		if parsed, err := parseTime(row.ExpiresAt.String); err == nil && !parsed.After(now) {
			state.expired = true
		}
	}
	return state
}

type SysfsCounterReader struct {
	Root string
}

func (reader SysfsCounterReader) ReadCounter(_ context.Context, locationID string) (Counter, error) {
	root := reader.Root
	if root == "" {
		root = "/sys/class/net"
	}
	names := netstack.NamesForLocation(locationID)
	statsDir := filepath.Join(root, names.HostVeth, "statistics")
	rx, err := readInt64(filepath.Join(statsDir, "rx_bytes"))
	if err != nil {
		return Counter{}, err
	}
	tx, err := readInt64(filepath.Join(statsDir, "tx_bytes"))
	if err != nil {
		return Counter{}, err
	}
	return Counter{RXBytes: rx, TXBytes: tx}, nil
}

func readInt64(path string) (int64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	value, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", path, err)
	}
	if value < 0 {
		return 0, fmt.Errorf("%s is negative", path)
	}
	return value, nil
}

func isMissingCounter(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, os.ErrNotExist)
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) (time.Time, error) {
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed, nil
	}
	return time.Parse("2006-01-02 15:04:05", value)
}

package observability_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"olcpanel/internal/observability"
)

func TestFileSinkAppendsJSONLAndQueryFiltersEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "panel.log")
	sink := observability.NewFileSink(path)
	base := time.Date(2026, 5, 28, 10, 0, 0, 0, time.UTC)
	entries := []observability.LogEntry{
		{Time: base, Level: "info", Source: "panel", ClientID: "cl_1", LocationID: "loc_1", Message: "started panel"},
		{Time: base.Add(time.Minute), Level: "warn", Source: "olcrtc_stdout", ClientID: "cl_1", LocationID: "loc_2", Message: "quota near limit", Attrs: map[string]any{"usage": float64(90)}},
		{Time: base.Add(2 * time.Minute), Level: "error", Source: "olcrtc_stderr", ClientID: "cl_2", LocationID: "loc_3", Message: "child failed"},
	}
	for _, entry := range entries {
		if err := sink.Append(context.Background(), entry); err != nil {
			t.Fatalf("Append returned error: %v", err)
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 {
		t.Fatalf("wrote %d lines, want 3: %s", len(lines), data)
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &raw); err != nil {
		t.Fatalf("second line is not JSON: %v", err)
	}
	if raw["source"] != "olcrtc_stdout" || raw["message"] != "quota near limit" {
		t.Fatalf("line = %#v, want stdout quota entry", raw)
	}

	result, err := sink.Query(context.Background(), observability.LogQuery{
		Level:      "warn",
		Source:     "olcrtc_stdout",
		ClientID:   "cl_1",
		LocationID: "loc_2",
		Since:      base.Add(-time.Second),
		Until:      base.Add(90 * time.Second),
		Query:      "quota",
		Limit:      10,
	})
	if err != nil {
		t.Fatalf("Query returned error: %v", err)
	}
	if len(result) != 1 || result[0].Message != "quota near limit" {
		t.Fatalf("query result = %#v, want one matching warning", result)
	}
}

func TestFileSinkQueryCapsLimitAndReturnsNewestEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "panel.log")
	sink := observability.NewFileSink(path)
	base := time.Date(2026, 5, 28, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 1005; i++ {
		if err := sink.Append(context.Background(), observability.LogEntry{
			Time:    base.Add(time.Duration(i) * time.Second),
			Level:   "info",
			Source:  "panel",
			Message: "entry",
		}); err != nil {
			t.Fatalf("Append(%d) returned error: %v", i, err)
		}
	}

	result, err := sink.Query(context.Background(), observability.LogQuery{Limit: 5000})
	if err != nil {
		t.Fatalf("Query returned error: %v", err)
	}
	if len(result) != 1000 {
		t.Fatalf("len(result) = %d, want cap of 1000", len(result))
	}
	if !result[0].Time.Equal(base.Add(5 * time.Second)) {
		t.Fatalf("first result time = %s, want newest 1000 entries after cap", result[0].Time)
	}
}

func TestFileSinkQueryMissingFileReturnsEmptyResult(t *testing.T) {
	sink := observability.NewFileSink(filepath.Join(t.TempDir(), "missing.log"))

	result, err := sink.Query(context.Background(), observability.LogQuery{Limit: 10})
	if err != nil {
		t.Fatalf("Query returned error: %v", err)
	}
	if len(result) != 0 {
		t.Fatalf("result = %#v, want empty result for missing file", result)
	}
}

func TestFileSinkRotatesAtConfiguredSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "panel.log")
	sink := observability.NewFileSinkWithMaxBytes(path, 80)

	if err := sink.Append(context.Background(), observability.LogEntry{Time: time.Date(2026, 5, 28, 10, 0, 0, 0, time.UTC), Level: "info", Source: "panel", Message: "first entry"}); err != nil {
		t.Fatalf("first Append returned error: %v", err)
	}
	if err := sink.Append(context.Background(), observability.LogEntry{Time: time.Date(2026, 5, 28, 10, 1, 0, 0, time.UTC), Level: "info", Source: "panel", Message: "second entry"}); err != nil {
		t.Fatalf("second Append returned error: %v", err)
	}

	rotated, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatalf("ReadFile rotated log returned error: %v", err)
	}
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile current log returned error: %v", err)
	}
	if !strings.Contains(string(rotated), "first entry") || !strings.Contains(string(current), "second entry") {
		t.Fatalf("rotated=%q current=%q, want first entry rotated and second current", rotated, current)
	}
}

func TestFormatTextReturnsCopyFriendlyLines(t *testing.T) {
	when := time.Date(2026, 5, 28, 10, 0, 0, 0, time.UTC)
	text := observability.FormatText([]observability.LogEntry{{
		Time:       when,
		Level:      "error",
		Source:     "olcrtc_stderr",
		ClientID:   "cl_1",
		LocationID: "loc_1",
		Message:    "failed to start",
	}})

	want := "2026-05-28T10:00:00Z error olcrtc_stderr client=cl_1 location=loc_1 failed to start\n"
	if text != want {
		t.Fatalf("text = %q, want %q", text, want)
	}
}

func TestFileSinkUnavailableErrorCanBeDetected(t *testing.T) {
	err := observability.ErrUnavailable
	if !errors.Is(err, observability.ErrUnavailable) {
		t.Fatal("ErrUnavailable should match itself")
	}
}

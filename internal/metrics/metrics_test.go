package metrics_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"olcpanel/internal/clients"
	"olcpanel/internal/metrics"
	"olcpanel/internal/storage"
	"olcpanel/internal/supervisor"
)

func TestSnapshotIncludesClientTrafficQuotaExpiryAndProcessCounts(t *testing.T) {
	db := testDB(t)
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	warningQuota := int64(100)
	warning, err := clients.CreateClient(context.Background(), db, clients.ClientInput{Name: "Warning", QuotaBytes: &warningQuota})
	if err != nil {
		t.Fatalf("CreateClient warning returned error: %v", err)
	}
	running, err := clients.CreateLocation(context.Background(), db, warning.ID, clients.LocationInput{Name: "Running", Provider: "wbstream", Transport: "datachannel"})
	if err != nil {
		t.Fatalf("CreateLocation running returned error: %v", err)
	}
	failed, err := clients.CreateLocation(context.Background(), db, warning.ID, clients.LocationInput{Name: "Failed", Provider: "wbstream", Transport: "datachannel"})
	if err != nil {
		t.Fatalf("CreateLocation failed returned error: %v", err)
	}
	expiredAt := now.Add(-time.Hour)
	expired, err := clients.CreateClient(context.Background(), db, clients.ClientInput{Name: "Expired", ExpiresAt: &expiredAt})
	if err != nil {
		t.Fatalf("CreateClient expired returned error: %v", err)
	}
	if _, err := clients.CreateLocation(context.Background(), db, expired.ID, clients.LocationInput{Name: "Pending", Provider: "wbstream", Transport: "datachannel"}); err != nil {
		t.Fatalf("CreateLocation pending returned error: %v", err)
	}
	if _, err := db.ExecContext(context.Background(), `UPDATE clients SET quota_used_bytes = 80 WHERE id = ?`, warning.ID); err != nil {
		t.Fatalf("mark warning quota: %v", err)
	}
	if _, err := db.ExecContext(context.Background(), `
INSERT INTO traffic_counters(node_id, client_id, location_id, rx_bytes, tx_bytes, period_start, updated_at)
VALUES ('local', ?, ?, 30, 50, ?, ?)`, warning.ID, running.ID, now.Add(-time.Minute).Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert traffic counter: %v", err)
	}

	snapshot, err := metrics.BuildSnapshot(context.Background(), db, metrics.Options{
		StartedAt:  now.Add(-2 * time.Hour),
		Now:        func() time.Time { return now },
		HostReader: fakeHostReader{},
		Statuses: fakeStatusProvider{statuses: map[string]supervisor.ProcessStatus{
			running.ID: supervisor.ProcessRunning,
			failed.ID:  supervisor.ProcessFailed,
		}},
	})
	if err != nil {
		t.Fatalf("BuildSnapshot returned error: %v", err)
	}

	if snapshot.Panel.UptimeSeconds != 7200 {
		t.Fatalf("uptime = %d, want 7200", snapshot.Panel.UptimeSeconds)
	}
	if snapshot.Clients.Total != 2 || snapshot.Clients.Enabled != 2 || snapshot.Clients.Expired != 1 {
		t.Fatalf("client counts = %#v, want total 2 enabled 2 expired 1", snapshot.Clients)
	}
	if snapshot.Locations.Total != 3 || snapshot.Processes.Running != 1 || snapshot.Processes.Failed != 1 || snapshot.Processes.Pending != 1 {
		t.Fatalf("location/process counts = %#v %#v, want 3 locations and running/failed/pending", snapshot.Locations, snapshot.Processes)
	}
	if snapshot.Traffic.TotalBytes != 80 || snapshot.Traffic.RXBytes != 30 || snapshot.Traffic.TXBytes != 50 {
		t.Fatalf("traffic = %#v, want quota and counter totals", snapshot.Traffic)
	}
	if snapshot.Quotas.Warning != 1 || snapshot.Quotas.Exceeded != 0 {
		t.Fatalf("quotas = %#v, want one warning and no exceeded", snapshot.Quotas)
	}
	warningSummary := findClientSummary(snapshot.PerClient, warning.ID)
	if warningSummary == nil || warningSummary.TrafficBytes != 80 || warningSummary.RXBytes != 30 || warningSummary.TXBytes != 50 || warningSummary.Processes.Failed != 1 {
		t.Fatalf("per-client = %#v, want warning client traffic/process summary", snapshot.PerClient)
	}
	if snapshot.Host.CPUPercent == nil || *snapshot.Host.CPUPercent != 12.5 {
		t.Fatalf("host = %#v, want fake host fields", snapshot.Host)
	}
}

func findClientSummary(summaries []metrics.ClientSummary, clientID string) *metrics.ClientSummary {
	for i := range summaries {
		if summaries[i].ClientID == clientID {
			return &summaries[i]
		}
	}
	return nil
}

type fakeStatusProvider struct {
	statuses map[string]supervisor.ProcessStatus
}

func (provider fakeStatusProvider) StatusSnapshot() map[string]supervisor.ProcessStatus {
	return provider.statuses
}

type fakeHostReader struct{}

func (fakeHostReader) ReadHost(context.Context) (metrics.HostSnapshot, error) {
	cpu := 12.5
	return metrics.HostSnapshot{CPUPercent: &cpu}, nil
}

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := storage.Open(context.Background(), "sqlite:///"+filepath.ToSlash(filepath.Join(t.TempDir(), "panel.db")))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := storage.Migrate(context.Background(), db); err != nil {
		t.Fatalf("Migrate returned error: %v", err)
	}
	return db
}

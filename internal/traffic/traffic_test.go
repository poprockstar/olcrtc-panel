package traffic_test

import (
	"context"
	"database/sql"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"olcpanel/internal/clients"
	"olcpanel/internal/netstack"
	"olcpanel/internal/storage"
	"olcpanel/internal/traffic"
)

func TestFirstSampleStoresBaselineWithoutUsage(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	client, location := seedClientLocation(t, db, int64Ptr(1000), nil)
	reader := fakeReader{values: map[string]traffic.Counter{
		location.ID: {RXBytes: 100, TXBytes: 200},
	}}
	sampler := traffic.NewSampler(db, reader, traffic.Options{Now: fixedClock()})

	if err := sampler.Sample(ctx); err != nil {
		t.Fatalf("Sample returned error: %v", err)
	}

	assertClientUsage(t, db, client.ID, 0)
	assertCounterState(t, db, location.ID, 100, 200, 0)
	assertTrafficCounterCount(t, db, 0)
}

func TestSecondSamplePersistsDeltaAndIncrementsClientUsage(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	client, location := seedClientLocation(t, db, int64Ptr(1000), nil)
	reader := fakeReader{values: map[string]traffic.Counter{
		location.ID: {RXBytes: 100, TXBytes: 200},
	}}
	sampler := traffic.NewSampler(db, reader, traffic.Options{Now: fixedClock()})
	if err := sampler.Sample(ctx); err != nil {
		t.Fatalf("first Sample returned error: %v", err)
	}
	reader.values[location.ID] = traffic.Counter{RXBytes: 150, TXBytes: 260}

	if err := sampler.Sample(ctx); err != nil {
		t.Fatalf("second Sample returned error: %v", err)
	}

	assertClientUsage(t, db, client.ID, 110)
	assertCounterState(t, db, location.ID, 150, 260, 0)
	assertTrafficCounterDelta(t, db, client.ID, location.ID, 50, 60)
}

func TestCounterResetUpdatesBaselineWithoutNegativeTraffic(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	client, location := seedClientLocation(t, db, int64Ptr(1000), nil)
	reader := fakeReader{values: map[string]traffic.Counter{
		location.ID: {RXBytes: 100, TXBytes: 200},
	}}
	sampler := traffic.NewSampler(db, reader, traffic.Options{Now: fixedClock()})
	if err := sampler.Sample(ctx); err != nil {
		t.Fatalf("first Sample returned error: %v", err)
	}
	reader.values[location.ID] = traffic.Counter{RXBytes: 20, TXBytes: 30}

	if err := sampler.Sample(ctx); err != nil {
		t.Fatalf("reset Sample returned error: %v", err)
	}

	assertClientUsage(t, db, client.ID, 0)
	assertCounterState(t, db, location.ID, 20, 30, 1)
	assertTrafficCounterCount(t, db, 0)
}

func TestMissingCounterSkipsLocationWithoutFailingSample(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	_, location := seedClientLocation(t, db, int64Ptr(1000), nil)
	reader := fakeReader{errs: map[string]error{
		location.ID: fs.ErrNotExist,
	}}
	sampler := traffic.NewSampler(db, reader, traffic.Options{Now: fixedClock()})

	if err := sampler.Sample(ctx); err != nil {
		t.Fatalf("Sample returned error: %v", err)
	}

	assertTrafficCounterCount(t, db, 0)
}

func TestQuotaCrossingTriggersReloadOnce(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	client, location := seedClientLocation(t, db, int64Ptr(100), nil)
	reader := fakeReader{values: map[string]traffic.Counter{
		location.ID: {RXBytes: 100, TXBytes: 200},
	}}
	var reloads int
	sampler := traffic.NewSampler(db, reader, traffic.Options{
		Now: fixedClock(),
		Reload: func(context.Context) error {
			reloads++
			return nil
		},
	})
	if err := sampler.Sample(ctx); err != nil {
		t.Fatalf("first Sample returned error: %v", err)
	}
	reader.values[location.ID] = traffic.Counter{RXBytes: 160, TXBytes: 250}
	if err := sampler.Sample(ctx); err != nil {
		t.Fatalf("quota crossing Sample returned error: %v", err)
	}
	reader.values[location.ID] = traffic.Counter{RXBytes: 170, TXBytes: 260}
	if err := sampler.Sample(ctx); err != nil {
		t.Fatalf("post-crossing Sample returned error: %v", err)
	}

	if reloads != 1 {
		t.Fatalf("reloads = %d, want 1", reloads)
	}
	assertClientUsage(t, db, client.ID, 130)
}

func TestExpiryTransitionTriggersReloadOnNextTick(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	now := fixedNow()
	expiresAt := now.Add(time.Minute)
	_, location := seedClientLocation(t, db, nil, &expiresAt)
	reader := fakeReader{values: map[string]traffic.Counter{
		location.ID: {RXBytes: 100, TXBytes: 200},
	}}
	var reloads int
	currentTime := now
	sampler := traffic.NewSampler(db, reader, traffic.Options{
		Now: func() time.Time { return currentTime },
		Reload: func(context.Context) error {
			reloads++
			return nil
		},
	})
	if err := sampler.Sample(ctx); err != nil {
		t.Fatalf("pre-expiry Sample returned error: %v", err)
	}
	currentTime = expiresAt.Add(time.Second)
	if err := sampler.Sample(ctx); err != nil {
		t.Fatalf("post-expiry Sample returned error: %v", err)
	}
	if err := sampler.Sample(ctx); err != nil {
		t.Fatalf("second post-expiry Sample returned error: %v", err)
	}

	if reloads != 1 {
		t.Fatalf("reloads = %d, want 1", reloads)
	}
}

func TestSysfsCounterReaderReadsHostVethStatistics(t *testing.T) {
	locationID := "loc_reader"
	names := netstack.NamesForLocation(locationID)
	statsDir := filepath.Join(t.TempDir(), names.HostVeth, "statistics")
	if err := os.MkdirAll(statsDir, 0o755); err != nil {
		t.Fatalf("create stats dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(statsDir, "rx_bytes"), []byte("123\n"), 0o644); err != nil {
		t.Fatalf("write rx: %v", err)
	}
	if err := os.WriteFile(filepath.Join(statsDir, "tx_bytes"), []byte("456\n"), 0o644); err != nil {
		t.Fatalf("write tx: %v", err)
	}
	reader := traffic.SysfsCounterReader{Root: filepath.Dir(filepath.Dir(statsDir))}

	got, err := reader.ReadCounter(context.Background(), locationID)
	if err != nil {
		t.Fatalf("ReadCounter returned error: %v", err)
	}
	if got.RXBytes != 123 || got.TXBytes != 456 {
		t.Fatalf("counter = %#v, want rx 123 tx 456", got)
	}
}

type fakeReader struct {
	values map[string]traffic.Counter
	errs   map[string]error
}

func (reader fakeReader) ReadCounter(_ context.Context, locationID string) (traffic.Counter, error) {
	if err := reader.errs[locationID]; err != nil {
		return traffic.Counter{}, err
	}
	value, ok := reader.values[locationID]
	if !ok {
		return traffic.Counter{}, fs.ErrNotExist
	}
	return value, nil
}

func seedClientLocation(t *testing.T, db *sql.DB, quotaBytes *int64, expiresAt *time.Time) (clients.Client, clients.Location) {
	t.Helper()
	client, err := clients.CreateClient(context.Background(), db, clients.ClientInput{
		Name:       "Client",
		QuotaBytes: quotaBytes,
		ExpiresAt:  expiresAt,
	})
	if err != nil {
		t.Fatalf("CreateClient returned error: %v", err)
	}
	location, err := clients.CreateLocation(context.Background(), db, client.ID, clients.LocationInput{
		Name:      "Main",
		Provider:  "wbstream",
		Transport: "datachannel",
	})
	if err != nil {
		t.Fatalf("CreateLocation returned error: %v", err)
	}
	return client, location
}

func assertClientUsage(t *testing.T, db *sql.DB, clientID string, want int64) {
	t.Helper()
	var got int64
	if err := db.QueryRow(`SELECT quota_used_bytes FROM clients WHERE id = ?`, clientID).Scan(&got); err != nil {
		t.Fatalf("read client usage: %v", err)
	}
	if got != want {
		t.Fatalf("quota_used_bytes = %d, want %d", got, want)
	}
}

func assertCounterState(t *testing.T, db *sql.DB, locationID string, rx, tx, resetCount int64) {
	t.Helper()
	var gotRX, gotTX, gotResetCount int64
	if err := db.QueryRow(`
SELECT rx_bytes, tx_bytes, reset_count
FROM traffic_counter_state
WHERE node_id = 'local' AND location_id = ?`, locationID).Scan(&gotRX, &gotTX, &gotResetCount); err != nil {
		t.Fatalf("read counter state: %v", err)
	}
	if gotRX != rx || gotTX != tx || gotResetCount != resetCount {
		t.Fatalf("counter state = rx %d tx %d resets %d, want rx %d tx %d resets %d", gotRX, gotTX, gotResetCount, rx, tx, resetCount)
	}
}

func assertTrafficCounterCount(t *testing.T, db *sql.DB, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(`SELECT COUNT(*) FROM traffic_counters`).Scan(&got); err != nil {
		t.Fatalf("count traffic counters: %v", err)
	}
	if got != want {
		t.Fatalf("traffic counter count = %d, want %d", got, want)
	}
}

func assertTrafficCounterDelta(t *testing.T, db *sql.DB, clientID, locationID string, rx, tx int64) {
	t.Helper()
	var gotRX, gotTX int64
	if err := db.QueryRow(`
SELECT rx_bytes, tx_bytes
FROM traffic_counters
WHERE node_id = 'local' AND client_id = ? AND location_id = ?`, clientID, locationID).Scan(&gotRX, &gotTX); err != nil {
		t.Fatalf("read traffic counter delta: %v", err)
	}
	if gotRX != rx || gotTX != tx {
		t.Fatalf("traffic delta = rx %d tx %d, want rx %d tx %d", gotRX, gotTX, rx, tx)
	}
}

func int64Ptr(value int64) *int64 {
	return &value
}

func fixedClock() func() time.Time {
	return fixedNow
}

func fixedNow() time.Time {
	return time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
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

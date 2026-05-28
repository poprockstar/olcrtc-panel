package storage_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"olcpanel/internal/storage"
)

func TestMigrateCreatesPhase2Tables(t *testing.T) {
	db := openMigratedSQLite(t)

	for _, table := range []string{
		"schema_migrations",
		"users",
		"sessions",
		"api_keys",
		"clients",
		"locations",
		"traffic_counters",
		"traffic_counter_state",
		"settings",
		"nodes",
		"backups",
		"integration_mappings",
	} {
		if !tableExists(t, db, table) {
			t.Fatalf("expected table %q to exist", table)
		}
	}
}

func TestPhase9MigrationAddsTrafficCounterStateAndIndexes(t *testing.T) {
	db := openMigratedSQLite(t)

	if !tableExists(t, db, "traffic_counter_state") {
		t.Fatal("traffic_counter_state table does not exist")
	}
	for _, column := range []string{"node_id", "location_id", "rx_bytes", "tx_bytes", "last_sampled_at", "reset_count"} {
		if !columnExists(t, db, "traffic_counter_state", column) {
			t.Fatalf("traffic_counter_state.%s column does not exist", column)
		}
	}
	for _, index := range []string{
		"idx_traffic_counters_client_location_time",
		"idx_traffic_counters_location_time",
	} {
		if !indexExists(t, db, index) {
			t.Fatalf("expected index %q to exist", index)
		}
	}
}

func TestPhase5MigrationBackfillsSubscriptionTokens(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, sqliteURL(t))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.ExecContext(ctx, `
CREATE TABLE schema_migrations (
	version INTEGER PRIMARY KEY,
	name TEXT NOT NULL,
	applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
)`); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}
	for _, migration := range []string{
		"001_phase2_core.sql",
		"002_phase3_auth.sql",
		"003_phase4_clients_locations.sql",
	} {
		applyMigrationFile(t, db, migration)
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO clients(id, node_id, name, enabled, quota_used_bytes, updated_at)
VALUES ('cl_existing', 'local', 'Existing', 1, 0, CURRENT_TIMESTAMP)`); err != nil {
		t.Fatalf("insert pre-phase5 client: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO schema_migrations(version, name)
VALUES (1, '001_phase2_core.sql'), (2, '002_phase3_auth.sql'), (3, '003_phase4_clients_locations.sql')`); err != nil {
		t.Fatalf("record earlier migrations: %v", err)
	}

	if err := storage.Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate returned error: %v", err)
	}

	var token string
	if err := db.QueryRowContext(ctx, `SELECT subscription_token FROM clients WHERE id = 'cl_existing'`).Scan(&token); err != nil {
		t.Fatalf("read subscription token: %v", err)
	}
	if !strings.HasPrefix(token, "sub_") {
		t.Fatalf("subscription token = %q, want sub_ prefix", token)
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO clients(id, node_id, name, enabled, quota_used_bytes, subscription_token, updated_at)
VALUES ('cl_duplicate', 'local', 'Duplicate', 1, 0, ?, CURRENT_TIMESTAMP)`, token); err == nil {
		t.Fatal("insert duplicate subscription token succeeded, want unique constraint error")
	}
}

func TestMigrateIsIdempotentAndSeedsDefaultsOnce(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, sqliteURL(t))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	for i := 0; i < 2; i++ {
		if err := storage.Migrate(ctx, db); err != nil {
			t.Fatalf("Migrate pass %d returned error: %v", i+1, err)
		}
	}

	assertCount(t, db, "nodes", 1)
	assertCount(t, db, "settings", 4)

	settings, err := storage.GetSettings(ctx, db)
	if err != nil {
		t.Fatalf("GetSettings returned error: %v", err)
	}
	if settings.UILocale != "en" {
		t.Fatalf("UILocale = %q, want en", settings.UILocale)
	}
	if settings.PublicClientEndpointEnabled {
		t.Fatal("PublicClientEndpointEnabled = true, want false")
	}
	if settings.BackupPath != "/var/lib/olcpanel/backups" {
		t.Fatalf("BackupPath = %q, want default backup path", settings.BackupPath)
	}
	if settings.QuotaLockMode != "stop" {
		t.Fatalf("QuotaLockMode = %q, want stop", settings.QuotaLockMode)
	}
}

func TestPhase8MigrationAddsNullableLocationSpeedLimit(t *testing.T) {
	db := openMigratedSQLite(t)

	if !columnExists(t, db, "locations", "speed_limit_bps") {
		t.Fatal("locations.speed_limit_bps column does not exist")
	}

	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `
INSERT INTO clients(id, node_id, name, enabled, quota_used_bytes, subscription_token, updated_at)
VALUES ('cl_speed', 'local', 'Speed', 1, 0, 'sub_speed', CURRENT_TIMESTAMP)`); err != nil {
		t.Fatalf("insert client: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO locations(id, node_id, client_id, name, enabled, provider, transport, room_id, crypto_key, transport_payload, dns, updated_at)
VALUES ('loc_speed', 'local', 'cl_speed', 'Speed', 1, 'wbstream', 'datachannel', 'room', '0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef', '{}', '8.8.8.8:53', CURRENT_TIMESTAMP)`); err != nil {
		t.Fatalf("insert location without speed limit: %v", err)
	}
	var speedLimit sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT speed_limit_bps FROM locations WHERE id = 'loc_speed'`).Scan(&speedLimit); err != nil {
		t.Fatalf("read speed limit: %v", err)
	}
	if speedLimit.Valid {
		t.Fatalf("speed_limit_bps valid = true, want null")
	}
}

func TestPutSettingsPersistsCoreSettings(t *testing.T) {
	ctx := context.Background()
	db := openMigratedSQLite(t)

	want := storage.Settings{
		UILocale:                    "ru",
		PublicClientEndpointEnabled: true,
		BackupPath:                  "/srv/olcpanel/backups",
		QuotaLockMode:               "disable_traffic",
	}
	if err := storage.PutSettings(ctx, db, want); err != nil {
		t.Fatalf("PutSettings returned error: %v", err)
	}

	got, err := storage.GetSettings(ctx, db)
	if err != nil {
		t.Fatalf("GetSettings returned error: %v", err)
	}
	if got != want {
		t.Fatalf("settings = %#v, want %#v", got, want)
	}
}

func TestOpenSQLiteEnablesConnectionPragmas(t *testing.T) {
	db := openMigratedSQLite(t)

	var foreignKeys int
	if err := db.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		t.Fatalf("read foreign_keys pragma: %v", err)
	}
	if foreignKeys != 1 {
		t.Fatalf("foreign_keys = %d, want 1", foreignKeys)
	}

	var busyTimeout int
	if err := db.QueryRow(`PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
		t.Fatalf("read busy_timeout pragma: %v", err)
	}
	if busyTimeout < 5000 {
		t.Fatalf("busy_timeout = %d, want at least 5000", busyTimeout)
	}
}

func openMigratedSQLite(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, sqliteURL(t))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := storage.Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate returned error: %v", err)
	}
	return db
}

func sqliteURL(t *testing.T) string {
	t.Helper()
	return "sqlite:///" + filepath.ToSlash(filepath.Join(t.TempDir(), "panel.db"))
}

func tableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var found string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&found)
	if err == sql.ErrNoRows {
		return false
	}
	if err != nil {
		t.Fatalf("table lookup for %q failed: %v", name, err)
	}
	return true
}

func columnExists(t *testing.T, db *sql.DB, table, column string) bool {
	t.Helper()
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		t.Fatalf("table_info %s failed: %v", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			t.Fatalf("scan table_info %s: %v", table, err)
		}
		if name == column {
			return true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate table_info %s: %v", table, err)
	}
	return false
}

func indexExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var found string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?`, name).Scan(&found)
	if err == sql.ErrNoRows {
		return false
	}
	if err != nil {
		t.Fatalf("index lookup for %q failed: %v", name, err)
	}
	return true
}

func assertCount(t *testing.T, db *sql.DB, table string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&got); err != nil {
		t.Fatalf("count %s failed: %v", table, err)
	}
	if got != want {
		t.Fatalf("%s count = %d, want %d", table, got, want)
	}
}

func applyMigrationFile(t *testing.T, db *sql.DB, name string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("migrations", name))
	if err != nil {
		t.Fatalf("read migration %s: %v", name, err)
	}
	if _, err := db.Exec(string(data)); err != nil {
		t.Fatalf("apply migration %s: %v", name, err)
	}
}

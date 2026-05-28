package storage_test

import (
	"context"
	"database/sql"
	"path/filepath"
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

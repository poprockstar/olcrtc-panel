package storage

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"runtime"
	"sort"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"
)

type Dialect string

const (
	DialectSQLite     Dialect = "sqlite"
	DialectPostgreSQL Dialect = "postgresql"
)

type DatabaseInfo struct {
	Dialect Dialect
	DSN     string
}

type Settings struct {
	UILocale                    string `json:"ui_locale"`
	PublicClientEndpointEnabled bool   `json:"public_client_endpoint_enabled"`
	BackupPath                  string `json:"backup_path"`
	QuotaLockMode               string `json:"quota_lock_mode"`
}

//go:embed migrations/*.sql
var migrationFiles embed.FS

func ParseDatabaseURL(raw string) (DatabaseInfo, error) {
	if strings.TrimSpace(raw) == "" {
		return DatabaseInfo{}, errors.New("database URL is required")
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return DatabaseInfo{}, fmt.Errorf("parse database URL: %w", err)
	}

	switch parsed.Scheme {
	case "sqlite":
		dsn, err := sqliteDSN(parsed)
		if err != nil {
			return DatabaseInfo{}, err
		}
		return DatabaseInfo{Dialect: DialectSQLite, DSN: dsn}, nil
	case "postgres", "postgresql":
		return DatabaseInfo{Dialect: DialectPostgreSQL, DSN: raw}, nil
	default:
		return DatabaseInfo{}, fmt.Errorf("unsupported database URL scheme %q", parsed.Scheme)
	}
}

func Open(ctx context.Context, databaseURL string) (*sql.DB, error) {
	info, err := ParseDatabaseURL(databaseURL)
	if err != nil {
		return nil, err
	}
	if info.Dialect != DialectSQLite {
		return nil, fmt.Errorf("%s database URLs are recognized but not enabled in Phase 2", info.Dialect)
	}

	db, err := sql.Open("sqlite", info.DSN)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys = ON`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("enable sqlite foreign keys: %w", err)
	}
	if _, err := db.ExecContext(ctx, `PRAGMA busy_timeout = 5000`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("set sqlite busy timeout: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite database: %w", err)
	}
	return db, nil
}

func Migrate(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return errors.New("database is required")
	}

	migrations, err := loadMigrations()
	if err != nil {
		return err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
	version INTEGER PRIMARY KEY,
	name TEXT NOT NULL,
	applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
)`); err != nil {
		return fmt.Errorf("ensure schema_migrations: %w", err)
	}

	var current int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&current); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}

	for _, migration := range migrations {
		if migration.version <= current {
			continue
		}
		if _, err := tx.ExecContext(ctx, migration.sql); err != nil {
			return fmt.Errorf("apply migration %s: %w", migration.name, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, name) VALUES (?, ?)`, migration.version, migration.name); err != nil {
			return fmt.Errorf("record migration %s: %w", migration.name, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migrations: %w", err)
	}
	return nil
}

func DefaultSettings() Settings {
	return Settings{
		UILocale:                    "en",
		PublicClientEndpointEnabled: false,
		BackupPath:                  "/var/lib/olcpanel/backups",
		QuotaLockMode:               "stop",
	}
}

func GetSettings(ctx context.Context, db *sql.DB) (Settings, error) {
	rows, err := db.QueryContext(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return Settings{}, fmt.Errorf("read settings: %w", err)
	}
	defer rows.Close()

	settings := DefaultSettings()
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return Settings{}, fmt.Errorf("scan settings: %w", err)
		}
		switch key {
		case "ui_locale":
			settings.UILocale = value
		case "public_client_endpoint_enabled":
			settings.PublicClientEndpointEnabled = value == "true"
		case "backup_path":
			settings.BackupPath = value
		case "quota_lock_mode":
			settings.QuotaLockMode = value
		}
	}
	if err := rows.Err(); err != nil {
		return Settings{}, fmt.Errorf("iterate settings: %w", err)
	}
	return settings, nil
}

func PutSettings(ctx context.Context, db *sql.DB, settings Settings) error {
	if err := ValidateSettings(settings); err != nil {
		return err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin settings transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	values := map[string]string{
		"ui_locale":                      settings.UILocale,
		"public_client_endpoint_enabled": strconv.FormatBool(settings.PublicClientEndpointEnabled),
		"backup_path":                    settings.BackupPath,
		"quota_lock_mode":                settings.QuotaLockMode,
	}

	for key, value := range values {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO settings(key, value, updated_at)
VALUES (?, ?, CURRENT_TIMESTAMP)
ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP`, key, value); err != nil {
			return fmt.Errorf("write setting %s: %w", key, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit settings: %w", err)
	}
	return nil
}

func ValidateSettings(settings Settings) error {
	if settings.UILocale != "en" && settings.UILocale != "ru" {
		return errors.New("ui_locale must be en or ru")
	}
	if strings.TrimSpace(settings.BackupPath) == "" {
		return errors.New("backup_path is required")
	}
	if settings.QuotaLockMode != "stop" && settings.QuotaLockMode != "disable_traffic" {
		return errors.New("quota_lock_mode must be stop or disable_traffic")
	}
	return nil
}

type migration struct {
	version int
	name    string
	sql     string
}

func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read embedded migrations: %w", err)
	}

	migrations := make([]migration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		versionText, _, ok := strings.Cut(entry.Name(), "_")
		if !ok {
			return nil, fmt.Errorf("migration %q must start with numeric version", entry.Name())
		}
		version, err := strconv.Atoi(versionText)
		if err != nil {
			return nil, fmt.Errorf("parse migration version %q: %w", entry.Name(), err)
		}
		data, err := fs.ReadFile(migrationFiles, "migrations/"+entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", entry.Name(), err)
		}
		migrations = append(migrations, migration{
			version: version,
			name:    entry.Name(),
			sql:     string(data),
		})
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].version < migrations[j].version
	})
	if len(migrations) == 0 {
		return nil, errors.New("no migrations embedded")
	}
	return migrations, nil
}

func sqliteDSN(parsed *url.URL) (string, error) {
	path := parsed.Path
	if path == "" && parsed.Host != "" {
		path = parsed.Host
	}
	if parsed.Host != "" && parsed.Path != "" {
		path = parsed.Host + parsed.Path
	}
	if path == "" {
		return "", errors.New("sqlite database path is required")
	}

	unescaped, err := url.PathUnescape(path)
	if err != nil {
		return "", fmt.Errorf("decode sqlite path: %w", err)
	}
	if strings.HasPrefix(unescaped, "//") {
		unescaped = strings.TrimPrefix(unescaped, "/")
	}
	if runtime.GOOS == "windows" && len(unescaped) >= 3 && unescaped[0] == '/' && unescaped[2] == ':' {
		unescaped = unescaped[1:]
	}
	if parsed.RawQuery != "" {
		unescaped += "?" + parsed.RawQuery
	}
	return unescaped, nil
}

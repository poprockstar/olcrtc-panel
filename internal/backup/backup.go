package backup

import (
	"archive/zip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"olcpanel/internal/clients"
	"olcpanel/internal/storage"
	"olcpanel/internal/supervisor"
)

const (
	localNodeID        = "local"
	backupFormat       = "olcpanel-backup-v1"
	panelJSONFormat    = "olcpanel-panel-json-v1"
	backupVersion      = 1
	archiveDBName      = "panel.db"
	archiveManifest    = "manifest.json"
	defaultExportName  = "olcpanel-export.json"
	defaultArchiveMode = 0o600
)

type CreateOptions struct {
	DatabaseURL string
	OutputPath  string
}

type Record struct {
	ID             int64      `json:"id"`
	NodeID         string     `json:"node_id"`
	Path           string     `json:"path"`
	Status         string     `json:"status"`
	FormatVersion  int        `json:"format_version"`
	SizeBytes      int64      `json:"size_bytes"`
	ChecksumSHA256 string     `json:"checksum_sha256"`
	CreatedAt      time.Time  `json:"created_at"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
	ErrorMessage   string     `json:"error_message"`
}

type Manifest struct {
	Format        string           `json:"format"`
	FormatVersion int              `json:"format_version"`
	App           string           `json:"app"`
	CreatedAt     time.Time        `json:"created_at"`
	SchemaVersion int              `json:"schema_version"`
	Database      ManifestDatabase `json:"database"`
}

type ManifestDatabase struct {
	Path           string `json:"path"`
	SizeBytes      int64  `json:"size_bytes"`
	ChecksumSHA256 string `json:"checksum_sha256"`
}

type Runtime interface {
	StopAll(context.Context) error
	Reload(context.Context) (supervisor.ReloadResult, error)
}

type RestoreResult struct {
	BackupID int64                   `json:"backup_id"`
	Reload   supervisor.ReloadResult `json:"reload"`
}

type PanelJSON struct {
	Format        string           `json:"format"`
	FormatVersion int              `json:"format_version"`
	ExportedAt    time.Time        `json:"exported_at"`
	Settings      storage.Settings `json:"settings"`
	Clients       []PanelClient    `json:"clients"`
}

type PanelClient struct {
	ID                string          `json:"id,omitempty"`
	Name              string          `json:"name"`
	SubscriptionToken string          `json:"subscription_token,omitempty"`
	Enabled           bool            `json:"enabled"`
	ExpiresAt         *time.Time      `json:"expires_at"`
	QuotaBytes        *int64          `json:"quota_bytes"`
	Locations         []PanelLocation `json:"locations"`
}

type PanelLocation struct {
	ID               string          `json:"id,omitempty"`
	Name             string          `json:"name"`
	Enabled          bool            `json:"enabled"`
	Provider         string          `json:"provider"`
	Transport        string          `json:"transport"`
	RoomID           string          `json:"room_id"`
	CryptoKey        string          `json:"crypto_key"`
	TransportPayload json.RawMessage `json:"transport_payload"`
	DNS              string          `json:"dns"`
	SpeedLimitBPS    *int64          `json:"speed_limit_bps"`
}

type ImportOptions struct {
	ApplySettings bool
}

type ImportResult struct {
	ClientsCreated   int  `json:"clients_created"`
	LocationsCreated int  `json:"locations_created"`
	SettingsApplied  bool `json:"settings_applied"`
}

func Create(ctx context.Context, db *sql.DB, options CreateOptions) (Record, error) {
	if db == nil {
		return Record{}, errors.New("database is required")
	}
	if strings.TrimSpace(options.DatabaseURL) == "" {
		return Record{}, errors.New("database URL is required")
	}
	outputPath, err := archiveOutputPath(options.OutputPath)
	if err != nil {
		return Record{}, err
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return Record{}, fmt.Errorf("create backup directory: %w", err)
	}

	tempDir, err := os.MkdirTemp("", "olcpanel-backup-*")
	if err != nil {
		return Record{}, fmt.Errorf("create temp backup directory: %w", err)
	}
	defer os.RemoveAll(tempDir)
	snapshotPath := filepath.Join(tempDir, archiveDBName)
	if _, err := db.ExecContext(ctx, `VACUUM INTO ?`, snapshotPath); err != nil {
		return Record{}, fmt.Errorf("snapshot sqlite database: %w", err)
	}
	if err := migrateSnapshot(ctx, snapshotPath); err != nil {
		return Record{}, err
	}
	size, checksum, err := fileSizeAndChecksum(snapshotPath)
	if err != nil {
		return Record{}, err
	}
	schemaVersion, err := schemaVersion(ctx, db)
	if err != nil {
		return Record{}, err
	}
	now := time.Now().UTC()
	manifest := Manifest{
		Format:        backupFormat,
		FormatVersion: backupVersion,
		App:           "olcpanel",
		CreatedAt:     now,
		SchemaVersion: schemaVersion,
		Database: ManifestDatabase{
			Path:           archiveDBName,
			SizeBytes:      size,
			ChecksumSHA256: checksum,
		},
	}
	if err := writeArchive(outputPath, manifest, snapshotPath); err != nil {
		return Record{}, err
	}
	record, err := insertRecord(ctx, db, outputPath, "completed", backupVersion, size, checksum, &now, "")
	if err != nil {
		return Record{}, err
	}
	return record, nil
}

func List(ctx context.Context, db *sql.DB) ([]Record, error) {
	rows, err := db.QueryContext(ctx, `
SELECT id, node_id, path, status, format_version, size_bytes, checksum_sha256, created_at, completed_at, error_message
FROM backups
WHERE node_id = ?
ORDER BY created_at DESC, id DESC`, localNodeID)
	if err != nil {
		return nil, fmt.Errorf("list backups: %w", err)
	}
	defer rows.Close()
	var records []Record
	for rows.Next() {
		record, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate backups: %w", err)
	}
	return records, nil
}

func ValidateArchive(path string) (Manifest, error) {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("open backup archive: %w", err)
	}
	defer reader.Close()
	manifest, err := readManifest(&reader.Reader)
	if err != nil {
		return Manifest{}, err
	}
	if manifest.Format != backupFormat || manifest.FormatVersion != backupVersion {
		return Manifest{}, errors.New("unsupported backup format")
	}
	dbFile := zipEntry(&reader.Reader, archiveDBName)
	if dbFile == nil {
		return Manifest{}, errors.New("backup archive missing panel.db")
	}
	size, checksum, err := zipEntrySizeAndChecksum(dbFile)
	if err != nil {
		return Manifest{}, err
	}
	if manifest.Database.SizeBytes != size || manifest.Database.ChecksumSHA256 != checksum {
		return Manifest{}, errors.New("backup database checksum mismatch")
	}
	return manifest, nil
}

func RestoreKnown(ctx context.Context, db *sql.DB, backupID int64, runtime Runtime) (RestoreResult, error) {
	if db == nil {
		return RestoreResult{}, errors.New("database is required")
	}
	if backupID <= 0 {
		return RestoreResult{}, errors.New("backup_id must be positive")
	}
	record, err := getRecord(ctx, db, backupID)
	if err != nil {
		return RestoreResult{}, err
	}
	if record.Status != "completed" {
		return RestoreResult{}, errors.New("backup is not completed")
	}
	tempPath, cleanup, err := extractMigratedDatabase(ctx, record.Path)
	if err != nil {
		return RestoreResult{}, err
	}
	defer cleanup()
	if runtime != nil {
		if err := runtime.StopAll(ctx); err != nil {
			return RestoreResult{}, fmt.Errorf("stop runtime before restore: %w", err)
		}
	}
	if err := replaceDatabaseState(ctx, db, tempPath); err != nil {
		return RestoreResult{}, err
	}
	if err := storage.Migrate(ctx, db); err != nil {
		return RestoreResult{}, fmt.Errorf("migrate restored database: %w", err)
	}
	var reload supervisor.ReloadResult
	if runtime != nil {
		reload, err = runtime.Reload(ctx)
		if err != nil {
			return RestoreResult{}, fmt.Errorf("reload runtime after restore: %w", err)
		}
	}
	return RestoreResult{BackupID: backupID, Reload: reload}, nil
}

func RestoreFile(ctx context.Context, databaseURL, archivePath string) error {
	tempPath, cleanup, err := extractMigratedDatabase(ctx, archivePath)
	if err != nil {
		return err
	}
	defer cleanup()
	dbPath, err := sqlitePathFromDatabaseURL(databaseURL)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return fmt.Errorf("create database directory: %w", err)
	}
	return copyFile(dbPath, tempPath, defaultArchiveMode)
}

func ExportPanel(ctx context.Context, db *sql.DB) (PanelJSON, error) {
	settings, err := storage.GetSettings(ctx, db)
	if err != nil {
		return PanelJSON{}, err
	}
	clientRows, err := clients.ListClients(ctx, db)
	if err != nil {
		return PanelJSON{}, err
	}
	doc := PanelJSON{
		Format:        panelJSONFormat,
		FormatVersion: backupVersion,
		ExportedAt:    time.Now().UTC(),
		Settings:      settings,
		Clients:       make([]PanelClient, 0, len(clientRows)),
	}
	for _, client := range clientRows {
		panelClient := PanelClient{
			ID:                client.ID,
			Name:              client.Name,
			SubscriptionToken: client.SubscriptionToken,
			Enabled:           client.Enabled,
			ExpiresAt:         client.ExpiresAt,
			QuotaBytes:        client.QuotaBytes,
		}
		locations, err := clients.ListLocations(ctx, db, client.ID)
		if err != nil {
			return PanelJSON{}, err
		}
		for _, location := range locations {
			panelClient.Locations = append(panelClient.Locations, PanelLocation{
				ID:               location.ID,
				Name:             location.Name,
				Enabled:          location.Enabled,
				Provider:         location.Provider,
				Transport:        location.Transport,
				RoomID:           location.RoomID,
				CryptoKey:        location.CryptoKey,
				TransportPayload: json.RawMessage(location.TransportPayload),
				DNS:              location.DNS,
				SpeedLimitBPS:    location.SpeedLimitBPS,
			})
		}
		doc.Clients = append(doc.Clients, panelClient)
	}
	return doc, nil
}

func ImportPanel(ctx context.Context, db *sql.DB, doc PanelJSON, options ImportOptions) (ImportResult, error) {
	if doc.Format != panelJSONFormat || doc.FormatVersion != backupVersion {
		return ImportResult{}, errors.New("unsupported panel JSON format")
	}
	if err := validatePanelJSON(doc, options.ApplySettings); err != nil {
		return ImportResult{}, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return ImportResult{}, fmt.Errorf("begin import: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if options.ApplySettings {
		if err := putSettingsTx(ctx, tx, doc.Settings); err != nil {
			return ImportResult{}, err
		}
	}
	result := ImportResult{SettingsApplied: options.ApplySettings}
	for _, panelClient := range doc.Clients {
		clientID, err := randomID("cl")
		if err != nil {
			return ImportResult{}, err
		}
		token, err := clients.GenerateSubscriptionToken()
		if err != nil {
			return ImportResult{}, err
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO clients(id, node_id, name, subscription_token, enabled, expires_at, quota_bytes, quota_used_bytes, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, 0, CURRENT_TIMESTAMP)`,
			clientID, localNodeID, strings.TrimSpace(panelClient.Name), token, boolInt(panelClient.Enabled), nullableTime(panelClient.ExpiresAt), nullableInt64(panelClient.QuotaBytes)); err != nil {
			return ImportResult{}, fmt.Errorf("insert imported client: %w", err)
		}
		result.ClientsCreated++
		for _, panelLocation := range panelClient.Locations {
			locationID, err := randomID("loc")
			if err != nil {
				return ImportResult{}, err
			}
			payload, _ := clients.NormalizeTransportPayload(panelLocation.Transport, string(panelLocation.TransportPayload))
			if _, err := tx.ExecContext(ctx, `
INSERT INTO locations(id, node_id, client_id, name, enabled, provider, transport, room_id, crypto_key, transport_payload, dns, speed_limit_bps, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
				locationID, localNodeID, clientID, strings.TrimSpace(panelLocation.Name), boolInt(panelLocation.Enabled), panelLocation.Provider, panelLocation.Transport, strings.TrimSpace(panelLocation.RoomID), strings.TrimSpace(panelLocation.CryptoKey), payload, normalizedDNS(panelLocation.DNS), nullableInt64(panelLocation.SpeedLimitBPS)); err != nil {
				return ImportResult{}, fmt.Errorf("insert imported location: %w", err)
			}
			result.LocationsCreated++
		}
	}
	if err := tx.Commit(); err != nil {
		return ImportResult{}, fmt.Errorf("commit import: %w", err)
	}
	return result, nil
}

func DefaultExportFilename() string {
	return defaultExportName
}

func archiveOutputPath(output string) (string, error) {
	output = strings.TrimSpace(output)
	if output == "" {
		output = "."
	}
	if strings.EqualFold(filepath.Ext(output), ".zip") {
		return filepath.Abs(output)
	}
	name := "olcpanel-backup-" + time.Now().UTC().Format("20060102-150405") + ".zip"
	return filepath.Abs(filepath.Join(output, name))
}

func writeArchive(path string, manifest Manifest, dbPath string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, defaultArchiveMode)
	if err != nil {
		return fmt.Errorf("create backup archive: %w", err)
	}
	defer file.Close()
	writer := zip.NewWriter(file)
	manifestWriter, err := writer.Create(archiveManifest)
	if err != nil {
		_ = writer.Close()
		return fmt.Errorf("create manifest entry: %w", err)
	}
	encoder := json.NewEncoder(manifestWriter)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(manifest); err != nil {
		_ = writer.Close()
		return fmt.Errorf("write manifest: %w", err)
	}
	dbWriter, err := writer.Create(archiveDBName)
	if err != nil {
		_ = writer.Close()
		return fmt.Errorf("create database entry: %w", err)
	}
	dbFile, err := os.Open(dbPath)
	if err != nil {
		_ = writer.Close()
		return fmt.Errorf("open snapshot database: %w", err)
	}
	defer dbFile.Close()
	if _, err := io.Copy(dbWriter, dbFile); err != nil {
		_ = writer.Close()
		return fmt.Errorf("write database entry: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("close backup archive: %w", err)
	}
	return nil
}

func insertRecord(ctx context.Context, db *sql.DB, path, status string, version int, size int64, checksum string, completedAt *time.Time, message string) (Record, error) {
	var completed any
	if completedAt != nil {
		completed = formatTime(*completedAt)
	}
	result, err := db.ExecContext(ctx, `
INSERT INTO backups(node_id, path, status, format_version, size_bytes, checksum_sha256, completed_at, error_message)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, localNodeID, path, status, version, size, checksum, completed, message)
	if err != nil {
		return Record{}, fmt.Errorf("insert backup record: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Record{}, fmt.Errorf("read backup record id: %w", err)
	}
	return getRecord(ctx, db, id)
}

func getRecord(ctx context.Context, db *sql.DB, id int64) (Record, error) {
	row := db.QueryRowContext(ctx, `
SELECT id, node_id, path, status, format_version, size_bytes, checksum_sha256, created_at, completed_at, error_message
FROM backups
WHERE node_id = ? AND id = ?`, localNodeID, id)
	record, err := scanRecord(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, errors.New("backup not found")
	}
	return record, err
}

type scanner interface {
	Scan(dest ...any) error
}

func scanRecord(row scanner) (Record, error) {
	var record Record
	var created string
	var completed sql.NullString
	if err := row.Scan(&record.ID, &record.NodeID, &record.Path, &record.Status, &record.FormatVersion, &record.SizeBytes, &record.ChecksumSHA256, &created, &completed, &record.ErrorMessage); err != nil {
		return Record{}, err
	}
	record.CreatedAt, _ = parseTime(created)
	if completed.Valid {
		parsed, err := parseTime(completed.String)
		if err == nil {
			record.CompletedAt = &parsed
		}
	}
	return record, nil
}

func readManifest(reader *zip.Reader) (Manifest, error) {
	entry := zipEntry(reader, archiveManifest)
	if entry == nil {
		return Manifest{}, errors.New("backup archive missing manifest.json")
	}
	file, err := entry.Open()
	if err != nil {
		return Manifest{}, fmt.Errorf("open manifest: %w", err)
	}
	defer file.Close()
	var manifest Manifest
	if err := json.NewDecoder(file).Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode manifest: %w", err)
	}
	return manifest, nil
}

func zipEntry(reader *zip.Reader, name string) *zip.File {
	for _, file := range reader.File {
		if file.Name == name {
			return file
		}
	}
	return nil
}

func zipEntrySizeAndChecksum(file *zip.File) (int64, string, error) {
	reader, err := file.Open()
	if err != nil {
		return 0, "", fmt.Errorf("open %s: %w", file.Name, err)
	}
	defer reader.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, reader)
	if err != nil {
		return 0, "", fmt.Errorf("hash %s: %w", file.Name, err)
	}
	return size, hex.EncodeToString(hash.Sum(nil)), nil
}

func fileSizeAndChecksum(path string) (int64, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, "", fmt.Errorf("open file for checksum: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return 0, "", fmt.Errorf("hash file: %w", err)
	}
	return size, hex.EncodeToString(hash.Sum(nil)), nil
}

func schemaVersion(ctx context.Context, db *sql.DB) (int, error) {
	var version int
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	return version, nil
}

func extractMigratedDatabase(ctx context.Context, archivePath string) (string, func(), error) {
	if _, err := ValidateArchive(archivePath); err != nil {
		return "", nil, err
	}
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return "", nil, fmt.Errorf("open backup archive: %w", err)
	}
	defer reader.Close()
	entry := zipEntry(&reader.Reader, archiveDBName)
	if entry == nil {
		return "", nil, errors.New("backup archive missing panel.db")
	}
	tempDir, err := os.MkdirTemp("", "olcpanel-restore-*")
	if err != nil {
		return "", nil, fmt.Errorf("create restore temp directory: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(tempDir) }
	path := filepath.Join(tempDir, archiveDBName)
	if err := extractZipEntry(path, entry); err != nil {
		cleanup()
		return "", nil, err
	}
	if err := migrateSnapshot(ctx, path); err != nil {
		cleanup()
		return "", nil, err
	}
	return path, cleanup, nil
}

func extractZipEntry(path string, entry *zip.File) error {
	reader, err := entry.Open()
	if err != nil {
		return fmt.Errorf("open database entry: %w", err)
	}
	defer reader.Close()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, defaultArchiveMode)
	if err != nil {
		return fmt.Errorf("create extracted database: %w", err)
	}
	defer file.Close()
	if _, err := io.Copy(file, reader); err != nil {
		return fmt.Errorf("extract database: %w", err)
	}
	return nil
}

func migrateSnapshot(ctx context.Context, path string) error {
	db, err := storage.Open(ctx, "sqlite:///"+filepath.ToSlash(path))
	if err != nil {
		return fmt.Errorf("open snapshot database: %w", err)
	}
	defer db.Close()
	if err := storage.Migrate(ctx, db); err != nil {
		return fmt.Errorf("migrate snapshot database: %w", err)
	}
	return nil
}

func replaceDatabaseState(ctx context.Context, db *sql.DB, sourcePath string) error {
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		return fmt.Errorf("disable foreign keys for restore: %w", err)
	}
	defer db.ExecContext(context.Background(), `PRAGMA foreign_keys = ON`)
	if _, err := db.ExecContext(ctx, `ATTACH DATABASE ? AS restore_src`, sourcePath); err != nil {
		return fmt.Errorf("attach restore database: %w", err)
	}
	defer db.ExecContext(context.Background(), `DETACH DATABASE restore_src`)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin restore transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	tables := []string{
		"schema_migrations",
		"nodes",
		"users",
		"sessions",
		"api_keys",
		"clients",
		"locations",
		"traffic_counters",
		"settings",
		"backups",
		"integration_mappings",
		"traffic_counter_state",
	}
	for i := len(tables) - 1; i >= 0; i-- {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+quoteIdent(tables[i])); err != nil {
			return fmt.Errorf("clear table %s: %w", tables[i], err)
		}
	}
	for _, table := range tables {
		quoted := quoteIdent(table)
		if _, err := tx.ExecContext(ctx, `INSERT INTO `+quoted+` SELECT * FROM restore_src.`+quoted); err != nil {
			return fmt.Errorf("restore table %s: %w", table, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sqlite_sequence`); err == nil {
		_, _ = tx.ExecContext(ctx, `INSERT INTO sqlite_sequence SELECT * FROM restore_src.sqlite_sequence`)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit restore transaction: %w", err)
	}
	return nil
}

func validatePanelJSON(doc PanelJSON, validateSettings bool) error {
	if validateSettings {
		if err := storage.ValidateSettings(doc.Settings); err != nil {
			return err
		}
	}
	for _, client := range doc.Clients {
		if strings.TrimSpace(client.Name) == "" || len(strings.TrimSpace(client.Name)) > 120 {
			return errors.New("client name is required and must be at most 120 characters")
		}
		if client.QuotaBytes != nil && *client.QuotaBytes < 0 {
			return errors.New("quota_bytes must be null or non-negative")
		}
		for _, location := range client.Locations {
			if strings.TrimSpace(location.Name) == "" || len(strings.TrimSpace(location.Name)) > 120 {
				return errors.New("location name is required and must be at most 120 characters")
			}
			if _, err := clients.ValidateProviderTransport(strings.TrimSpace(location.Provider), strings.TrimSpace(location.Transport)); err != nil {
				return err
			}
			if _, err := clients.NormalizeTransportPayload(strings.TrimSpace(location.Transport), string(location.TransportPayload)); err != nil {
				return err
			}
			if strings.TrimSpace(location.CryptoKey) != "" {
				if len(strings.TrimSpace(location.CryptoKey)) != 64 {
					return errors.New("crypto_key must be 64 hex characters")
				}
				if _, err := hex.DecodeString(strings.TrimSpace(location.CryptoKey)); err != nil {
					return errors.New("crypto_key must be 64 hex characters")
				}
			}
			if _, _, err := net.SplitHostPort(normalizedDNS(location.DNS)); err != nil {
				return errors.New("dns must be host:port")
			}
			if location.SpeedLimitBPS != nil && *location.SpeedLimitBPS <= 0 {
				return errors.New("speed_limit_bps must be null or positive")
			}
		}
	}
	return nil
}

func putSettingsTx(ctx context.Context, tx *sql.Tx, settings storage.Settings) error {
	values := map[string]string{
		"ui_locale":                      settings.UILocale,
		"public_client_endpoint_enabled": fmt.Sprintf("%t", settings.PublicClientEndpointEnabled),
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
	return nil
}

func sqlitePathFromDatabaseURL(databaseURL string) (string, error) {
	info, err := storage.ParseDatabaseURL(databaseURL)
	if err != nil {
		return "", err
	}
	if info.Dialect != storage.DialectSQLite {
		return "", errors.New("only sqlite databases are supported")
	}
	path := info.DSN
	if idx := strings.Index(path, "?"); idx >= 0 {
		path = path[:idx]
	}
	if runtime.GOOS == "windows" {
		path = filepath.FromSlash(path)
	}
	if strings.TrimSpace(path) == "" {
		return "", errors.New("sqlite database path is required")
	}
	return path, nil
}

func copyFile(dst, src string, mode os.FileMode) error {
	input, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source file: %w", err)
	}
	defer input.Close()
	output, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("open destination file: %w", err)
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		return fmt.Errorf("copy file: %w", err)
	}
	if err := output.Close(); err != nil {
		return fmt.Errorf("close destination file: %w", err)
	}
	return nil
}

func randomID(prefix string) (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", fmt.Errorf("generate id: %w", err)
	}
	return prefix + "_" + hex.EncodeToString(data[:]), nil
}

func quoteIdent(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func nullableInt64(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func normalizedDNS(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "8.8.8.8:53"
	}
	return value
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

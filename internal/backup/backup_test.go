package backup_test

import (
	"archive/zip"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"olcpanel/internal/auth"
	"olcpanel/internal/backup"
	"olcpanel/internal/clients"
	"olcpanel/internal/storage"
	"olcpanel/internal/supervisor"
)

func TestCreateArchiveWritesManifestDatabaseAndRecord(t *testing.T) {
	ctx := context.Background()
	db, databaseURL := testDB(t)
	if _, err := auth.CreateFirstAdmin(ctx, db, "admin", "correct horse battery"); err != nil {
		t.Fatalf("CreateFirstAdmin returned error: %v", err)
	}
	outputDir := t.TempDir()

	record, err := backup.Create(ctx, db, backup.CreateOptions{
		DatabaseURL: databaseURL,
		OutputPath:  outputDir,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if record.ID == 0 || record.Status != "completed" || record.FormatVersion != 1 || record.SizeBytes == 0 || record.ChecksumSHA256 == "" {
		t.Fatalf("record = %#v, want completed v1 record with size and checksum", record)
	}
	if !strings.HasPrefix(record.Path, outputDir) {
		t.Fatalf("record path = %q, want under %q", record.Path, outputDir)
	}

	zr, err := zip.OpenReader(record.Path)
	if err != nil {
		t.Fatalf("open backup zip: %v", err)
	}
	defer zr.Close()
	entries := map[string]bool{}
	for _, file := range zr.File {
		entries[file.Name] = true
	}
	if !entries["manifest.json"] || !entries["panel.db"] {
		t.Fatalf("zip entries = %#v, want manifest.json and panel.db", entries)
	}

	manifest, err := backup.ValidateArchive(record.Path)
	if err != nil {
		t.Fatalf("ValidateArchive returned error: %v", err)
	}
	if manifest.Format != "olcpanel-backup-v1" || manifest.FormatVersion != 1 || manifest.Database.ChecksumSHA256 != record.ChecksumSHA256 {
		t.Fatalf("manifest = %#v, want olcpanel v1 manifest matching record checksum", manifest)
	}

	records, err := backup.List(ctx, db)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(records) != 1 || records[0].ID != record.ID {
		t.Fatalf("records = %#v, want created record", records)
	}
}

func TestValidateArchiveRejectsWrongFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.zip")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create zip: %v", err)
	}
	zw := zip.NewWriter(file)
	writer, err := zw.Create("manifest.json")
	if err != nil {
		t.Fatalf("create manifest: %v", err)
	}
	if _, err := writer.Write([]byte(`{"format":"something-else","format_version":99}`)); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip writer: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close zip file: %v", err)
	}

	if _, err := backup.ValidateArchive(path); err == nil {
		t.Fatal("ValidateArchive returned nil error for wrong format")
	}
}

func TestRestoreKnownBackupReplacesStateAndReloadsRuntime(t *testing.T) {
	ctx := context.Background()
	db, databaseURL := testDB(t)
	original, err := clients.CreateClient(ctx, db, clients.ClientInput{Name: "Original"})
	if err != nil {
		t.Fatalf("CreateClient original returned error: %v", err)
	}
	record, err := backup.Create(ctx, db, backup.CreateOptions{DatabaseURL: databaseURL, OutputPath: t.TempDir()})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if _, err := clients.CreateClient(ctx, db, clients.ClientInput{Name: "Later"}); err != nil {
		t.Fatalf("CreateClient later returned error: %v", err)
	}

	runtime := &fakeRuntime{}
	if _, err := backup.RestoreKnown(ctx, db, record.ID, runtime); err != nil {
		t.Fatalf("RestoreKnown returned error: %v", err)
	}
	if runtime.stopCalls != 1 || runtime.reloadCalls != 1 || runtime.order != "stop,reload" {
		t.Fatalf("runtime calls stop=%d reload=%d order=%q, want stop then reload", runtime.stopCalls, runtime.reloadCalls, runtime.order)
	}
	restored, err := clients.ListClients(ctx, db)
	if err != nil {
		t.Fatalf("ListClients returned error: %v", err)
	}
	if len(restored) != 1 || restored[0].ID != original.ID || restored[0].Name != "Original" {
		t.Fatalf("restored clients = %#v, want only original backup state", restored)
	}
}

func TestExportImportPanelJSONAppendsWithNewClientIdentityAndPreservedLocationSecrets(t *testing.T) {
	ctx := context.Background()
	source, _ := testDB(t)
	client, err := clients.CreateClient(ctx, source, clients.ClientInput{Name: "Source"})
	if err != nil {
		t.Fatalf("CreateClient returned error: %v", err)
	}
	location, err := clients.CreateLocation(ctx, source, client.ID, clients.LocationInput{
		Name:      "Main",
		Provider:  "wbstream",
		Transport: "datachannel",
		RoomID:    "room-fixed",
		CryptoKey: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	})
	if err != nil {
		t.Fatalf("CreateLocation returned error: %v", err)
	}

	doc, err := backup.ExportPanel(ctx, source)
	if err != nil {
		t.Fatalf("ExportPanel returned error: %v", err)
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal export: %v", err)
	}
	var decoded backup.PanelJSON
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal export: %v", err)
	}

	target, _ := testDB(t)
	result, err := backup.ImportPanel(ctx, target, decoded, backup.ImportOptions{})
	if err != nil {
		t.Fatalf("ImportPanel returned error: %v", err)
	}
	if result.ClientsCreated != 1 || result.LocationsCreated != 1 {
		t.Fatalf("result = %#v, want one client and one location", result)
	}
	imported, err := clients.ListClients(ctx, target)
	if err != nil {
		t.Fatalf("ListClients target returned error: %v", err)
	}
	if len(imported) != 1 || imported[0].ID == client.ID || imported[0].SubscriptionToken == client.SubscriptionToken {
		t.Fatalf("imported client = %#v, want new id and subscription token", imported)
	}
	importedLocations, err := clients.ListLocations(ctx, target, imported[0].ID)
	if err != nil {
		t.Fatalf("ListLocations target returned error: %v", err)
	}
	if len(importedLocations) != 1 || importedLocations[0].RoomID != location.RoomID || importedLocations[0].CryptoKey != location.CryptoKey {
		t.Fatalf("imported locations = %#v, want preserved room and crypto key", importedLocations)
	}
}

func TestImportPanelRejectsInvalidLocationBeforeCreatingRows(t *testing.T) {
	ctx := context.Background()
	db, _ := testDB(t)
	doc := backup.PanelJSON{
		Format:        "olcpanel-panel-json-v1",
		FormatVersion: 1,
		Settings:      storage.DefaultSettings(),
		Clients: []backup.PanelClient{{
			Name:    "Bad",
			Enabled: true,
			Locations: []backup.PanelLocation{{
				Name:      "Bad",
				Enabled:   true,
				Provider:  "telemost",
				Transport: "datachannel",
				DNS:       "8.8.8.8:53",
			}},
		}},
	}

	if _, err := backup.ImportPanel(ctx, db, doc, backup.ImportOptions{}); err == nil {
		t.Fatal("ImportPanel returned nil error for invalid provider/transport")
	}
	list, err := clients.ListClients(ctx, db)
	if err != nil {
		t.Fatalf("ListClients returned error: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("clients = %#v, want no partial import", list)
	}
}

func testDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "panel.db")
	databaseURL := "sqlite:///" + filepath.ToSlash(path)
	db, err := storage.Open(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := storage.Migrate(context.Background(), db); err != nil {
		t.Fatalf("Migrate returned error: %v", err)
	}
	return db, databaseURL
}

type fakeRuntime struct {
	stopCalls   int
	reloadCalls int
	order       string
}

func (runtime *fakeRuntime) StopAll(context.Context) error {
	runtime.stopCalls++
	runtime.order = appendOrder(runtime.order, "stop")
	return nil
}

func (runtime *fakeRuntime) Reload(context.Context) (supervisor.ReloadResult, error) {
	runtime.reloadCalls++
	runtime.order = appendOrder(runtime.order, "reload")
	return supervisor.ReloadResult{}, nil
}

func appendOrder(current, next string) string {
	if current == "" {
		return next
	}
	return current + "," + next
}

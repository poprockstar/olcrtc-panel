package server_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"testing/fstest"

	"olcpanel/internal/config"
	"olcpanel/internal/server"
	"olcpanel/internal/storage"
)

func TestStateEndpointReturnsFirstRunState(t *testing.T) {
	handler := server.New(config.Config{BindAddress: "127.0.0.1:8888"}, testAssets())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/state", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}

	var body struct {
		Service       string `json:"service"`
		APIVersion    string `json:"api_version"`
		SetupRequired bool   `json:"setup_required"`
		BindAddress   string `json:"bind_address"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}

	if body.Service != "olcpanel" {
		t.Fatalf("Service = %q, want olcpanel", body.Service)
	}
	if body.APIVersion != "v1" {
		t.Fatalf("APIVersion = %q, want v1", body.APIVersion)
	}
	if !body.SetupRequired {
		t.Fatalf("SetupRequired = false, want true for skeleton state")
	}
	if body.BindAddress != "127.0.0.1:8888" {
		t.Fatalf("BindAddress = %q, want default bind", body.BindAddress)
	}
}

func TestRootServesEmbeddedUI(t *testing.T) {
	handler := server.New(config.Config{BindAddress: "127.0.0.1:8888"}, testAssets())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Body.String(); got != "<!doctype html><title>OlcRTC Panel</title>" {
		t.Fatalf("body = %q, want embedded index", got)
	}
}

func TestGetSettingsReturnsDefaults(t *testing.T) {
	handler := server.New(config.Config{BindAddress: "127.0.0.1:8888"}, testAssets(), server.WithDatabase(testDB(t)))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/settings", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var body storage.Settings
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if body.UILocale != "en" || body.BackupPath != "/var/lib/olcpanel/backups" || body.QuotaLockMode != "stop" || body.PublicClientEndpointEnabled {
		t.Fatalf("settings = %#v, want migrated defaults", body)
	}
}

func TestPutSettingsPersistsValidSettings(t *testing.T) {
	db := testDB(t)
	handler := server.New(config.Config{BindAddress: "127.0.0.1:8888"}, testAssets(), server.WithDatabase(db))
	body := bytes.NewBufferString(`{"ui_locale":"ru","public_client_endpoint_enabled":true,"backup_path":"/srv/backups","quota_lock_mode":"disable_traffic"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", body)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	got, err := storage.GetSettings(context.Background(), db)
	if err != nil {
		t.Fatalf("GetSettings returned error: %v", err)
	}
	if got.UILocale != "ru" || got.BackupPath != "/srv/backups" || got.QuotaLockMode != "disable_traffic" || !got.PublicClientEndpointEnabled {
		t.Fatalf("settings = %#v, want PUT values", got)
	}
}

func TestPutSettingsRejectsInvalidValues(t *testing.T) {
	for name, body := range map[string]string{
		"locale":      `{"ui_locale":"de","public_client_endpoint_enabled":false,"backup_path":"/srv/backups","quota_lock_mode":"stop"}`,
		"quota mode":  `{"ui_locale":"en","public_client_endpoint_enabled":false,"backup_path":"/srv/backups","quota_lock_mode":"pause"}`,
		"backup path": `{"ui_locale":"en","public_client_endpoint_enabled":false,"backup_path":"","quota_lock_mode":"stop"}`,
	} {
		t.Run(name, func(t *testing.T) {
			handler := server.New(config.Config{BindAddress: "127.0.0.1:8888"}, testAssets(), server.WithDatabase(testDB(t)))
			req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", bytes.NewBufferString(body))
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d, body: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
			}
		})
	}
}

func testAssets() fs.FS {
	return fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<!doctype html><title>OlcRTC Panel</title>")},
	}
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

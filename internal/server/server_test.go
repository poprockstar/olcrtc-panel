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

	"olcpanel/internal/auth"
	"olcpanel/internal/config"
	"olcpanel/internal/server"
	"olcpanel/internal/storage"
)

func TestStateEndpointReturnsFirstRunState(t *testing.T) {
	handler := server.New(config.Config{BindAddress: "127.0.0.1:8888"}, testAssets(), server.WithDatabase(testDB(t)))
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
		t.Fatalf("SetupRequired = false, want true before first admin")
	}
	if body.BindAddress != "127.0.0.1:8888" {
		t.Fatalf("BindAddress = %q, want default bind", body.BindAddress)
	}
}

func TestSetupChangesStateAndReturnsSession(t *testing.T) {
	db := testDB(t)
	handler := server.New(config.Config{BindAddress: "127.0.0.1:8888"}, testAssets(), server.WithDatabase(db))

	setupReq := httptest.NewRequest(http.MethodPost, "/api/v1/setup", bytes.NewBufferString(`{"username":"admin","password":"correct horse battery"}`))
	setupReq.Header.Set("Content-Type", "application/json")
	setupRec := httptest.NewRecorder()
	handler.ServeHTTP(setupRec, setupReq)

	if setupRec.Code != http.StatusOK {
		t.Fatalf("setup status = %d, want %d, body: %s", setupRec.Code, http.StatusOK, setupRec.Body.String())
	}
	if len(setupRec.Result().Cookies()) == 0 {
		t.Fatal("setup did not set a session cookie")
	}
	var setupBody struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.Unmarshal(setupRec.Body.Bytes(), &setupBody); err != nil {
		t.Fatalf("setup response is not JSON: %v", err)
	}
	if setupBody.CSRFToken == "" {
		t.Fatal("setup response missing csrf_token")
	}

	stateReq := httptest.NewRequest(http.MethodGet, "/api/v1/state", nil)
	for _, cookie := range setupRec.Result().Cookies() {
		stateReq.AddCookie(cookie)
	}
	stateRec := httptest.NewRecorder()
	handler.ServeHTTP(stateRec, stateReq)

	var state struct {
		SetupRequired bool `json:"setup_required"`
		Authenticated bool `json:"authenticated"`
	}
	if err := json.Unmarshal(stateRec.Body.Bytes(), &state); err != nil {
		t.Fatalf("state response is not JSON: %v", err)
	}
	if state.SetupRequired || !state.Authenticated {
		t.Fatalf("state = %#v, want setup complete and authenticated", state)
	}
}

func TestSettingsBlockedBeforeSetupAndProtectedAfterSetup(t *testing.T) {
	db := testDB(t)
	handler := server.New(config.Config{BindAddress: "127.0.0.1:8888"}, testAssets(), server.WithDatabase(db))

	beforeReq := httptest.NewRequest(http.MethodGet, "/api/v1/settings", nil)
	beforeRec := httptest.NewRecorder()
	handler.ServeHTTP(beforeRec, beforeReq)
	if beforeRec.Code != http.StatusForbidden {
		t.Fatalf("before setup status = %d, want %d", beforeRec.Code, http.StatusForbidden)
	}

	if _, err := auth.CreateFirstAdmin(context.Background(), db, "admin", "correct horse battery"); err != nil {
		t.Fatalf("CreateFirstAdmin returned error: %v", err)
	}

	afterReq := httptest.NewRequest(http.MethodGet, "/api/v1/settings", nil)
	afterRec := httptest.NewRecorder()
	handler.ServeHTTP(afterRec, afterReq)
	if afterRec.Code != http.StatusUnauthorized {
		t.Fatalf("after setup unauthenticated status = %d, want %d", afterRec.Code, http.StatusUnauthorized)
	}
}

func TestSessionSettingsMutationRequiresCSRF(t *testing.T) {
	db := testDB(t)
	handler := server.New(config.Config{BindAddress: "127.0.0.1:8888"}, testAssets(), server.WithDatabase(db))
	cookies, csrf := loginSession(t, handler)

	body := `{"ui_locale":"ru","public_client_endpoint_enabled":true,"backup_path":"/srv/backups","quota_lock_mode":"disable_traffic"}`
	withoutReq := httptest.NewRequest(http.MethodPut, "/api/v1/settings", bytes.NewBufferString(body))
	for _, cookie := range cookies {
		withoutReq.AddCookie(cookie)
	}
	withoutRec := httptest.NewRecorder()
	handler.ServeHTTP(withoutRec, withoutReq)
	if withoutRec.Code != http.StatusForbidden {
		t.Fatalf("without csrf status = %d, want %d", withoutRec.Code, http.StatusForbidden)
	}

	withReq := httptest.NewRequest(http.MethodPut, "/api/v1/settings", bytes.NewBufferString(body))
	withReq.Header.Set("X-CSRF-Token", csrf)
	for _, cookie := range cookies {
		withReq.AddCookie(cookie)
	}
	withRec := httptest.NewRecorder()
	handler.ServeHTTP(withRec, withReq)
	if withRec.Code != http.StatusOK {
		t.Fatalf("with csrf status = %d, want %d, body: %s", withRec.Code, http.StatusOK, withRec.Body.String())
	}
}

func TestAPIKeyCanReadAndUpdateSettingsWithoutCSRF(t *testing.T) {
	db := testDB(t)
	handler := server.New(config.Config{BindAddress: "127.0.0.1:8888"}, testAssets(), server.WithDatabase(db))
	cookies, csrf := loginSession(t, handler)

	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/api-keys", bytes.NewBufferString(`{"name":"deploy"}`))
	createReq.Header.Set("X-CSRF-Token", csrf)
	for _, cookie := range cookies {
		createReq.AddCookie(cookie)
	}
	createRec := httptest.NewRecorder()
	handler.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusOK {
		t.Fatalf("create key status = %d, want %d, body: %s", createRec.Code, http.StatusOK, createRec.Body.String())
	}
	var created struct {
		ID    int64  `json:"id"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("create key response is not JSON: %v", err)
	}
	if created.ID == 0 || created.Token == "" {
		t.Fatalf("created key = %#v, want id and one-time token", created)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/api-keys", nil)
	for _, cookie := range cookies {
		listReq.AddCookie(cookie)
	}
	listRec := httptest.NewRecorder()
	handler.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list key status = %d, want %d", listRec.Code, http.StatusOK)
	}
	if bytes.Contains(listRec.Body.Bytes(), []byte(created.Token)) {
		t.Fatal("list response leaked raw API token")
	}

	body := `{"ui_locale":"ru","public_client_endpoint_enabled":true,"backup_path":"/srv/backups","quota_lock_mode":"disable_traffic"}`
	putReq := httptest.NewRequest(http.MethodPut, "/api/v1/settings", bytes.NewBufferString(body))
	putReq.Header.Set("Authorization", "Bearer "+created.Token)
	putRec := httptest.NewRecorder()
	handler.ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("api key put status = %d, want %d, body: %s", putRec.Code, http.StatusOK, putRec.Body.String())
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/api-keys/1", nil)
	deleteReq.Header.Set("X-CSRF-Token", csrf)
	for _, cookie := range cookies {
		deleteReq.AddCookie(cookie)
	}
	deleteRec := httptest.NewRecorder()
	handler.ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusNoContent {
		t.Fatalf("delete key status = %d, want %d", deleteRec.Code, http.StatusNoContent)
	}

	revokedReq := httptest.NewRequest(http.MethodGet, "/api/v1/settings", nil)
	revokedReq.Header.Set("Authorization", "Bearer "+created.Token)
	revokedRec := httptest.NewRecorder()
	handler.ServeHTTP(revokedRec, revokedReq)
	if revokedRec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked key status = %d, want %d", revokedRec.Code, http.StatusUnauthorized)
	}
}

func TestLogoutClearsSession(t *testing.T) {
	db := testDB(t)
	handler := server.New(config.Config{BindAddress: "127.0.0.1:8888"}, testAssets(), server.WithDatabase(db))
	cookies, csrf := loginSession(t, handler)

	logoutReq := httptest.NewRequest(http.MethodPost, "/api/v1/logout", nil)
	logoutReq.Header.Set("X-CSRF-Token", csrf)
	for _, cookie := range cookies {
		logoutReq.AddCookie(cookie)
	}
	logoutRec := httptest.NewRecorder()
	handler.ServeHTTP(logoutRec, logoutReq)
	if logoutRec.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d, want %d", logoutRec.Code, http.StatusNoContent)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/settings", nil)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("post-logout status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestLoginRateLimitReturnsTooManyRequests(t *testing.T) {
	db := testDB(t)
	handler := server.New(config.Config{BindAddress: "127.0.0.1:8888"}, testAssets(), server.WithDatabase(db))
	if _, err := auth.CreateFirstAdmin(context.Background(), db, "admin", "correct horse battery"); err != nil {
		t.Fatalf("CreateFirstAdmin returned error: %v", err)
	}

	var rec *httptest.ResponseRecorder
	for i := 0; i < 6; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/login", bytes.NewBufferString(`{"username":"admin","password":"wrong horse battery"}`))
		req.RemoteAddr = "198.51.100.8:1234"
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status after repeated failed logins = %d, want %d", rec.Code, http.StatusTooManyRequests)
	}
}

func TestSetupRateLimitReturnsTooManyRequests(t *testing.T) {
	handler := server.New(config.Config{BindAddress: "127.0.0.1:8888"}, testAssets(), server.WithDatabase(testDB(t)))

	var rec *httptest.ResponseRecorder
	for i := 0; i < 6; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/setup", bytes.NewBufferString(`{"username":"ad","password":"short"}`))
		req.RemoteAddr = "198.51.100.9:1234"
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status after repeated failed setup = %d, want %d", rec.Code, http.StatusTooManyRequests)
	}
}

func TestAPIKeyFailureRateLimitReturnsTooManyRequests(t *testing.T) {
	db := testDB(t)
	handler := server.New(config.Config{BindAddress: "127.0.0.1:8888"}, testAssets(), server.WithDatabase(db))
	if _, err := auth.CreateFirstAdmin(context.Background(), db, "admin", "correct horse battery"); err != nil {
		t.Fatalf("CreateFirstAdmin returned error: %v", err)
	}

	var rec *httptest.ResponseRecorder
	for i := 0; i < 11; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/settings", nil)
		req.RemoteAddr = "198.51.100.10:1234"
		req.Header.Set("Authorization", "Bearer olcp_invalid")
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status after repeated failed api keys = %d, want %d", rec.Code, http.StatusTooManyRequests)
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
	db := testDB(t)
	handler := server.New(config.Config{BindAddress: "127.0.0.1:8888"}, testAssets(), server.WithDatabase(db))
	cookies, _ := loginSession(t, handler)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/settings", nil)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
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
	cookies, csrf := loginSession(t, handler)
	body := bytes.NewBufferString(`{"ui_locale":"ru","public_client_endpoint_enabled":true,"backup_path":"/srv/backups","quota_lock_mode":"disable_traffic"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", body)
	req.Header.Set("X-CSRF-Token", csrf)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
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
			cookies, csrf := loginSession(t, handler)
			req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", bytes.NewBufferString(body))
			req.Header.Set("X-CSRF-Token", csrf)
			for _, cookie := range cookies {
				req.AddCookie(cookie)
			}
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d, body: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
			}
		})
	}
}

func loginSession(t *testing.T, handler http.Handler) ([]*http.Cookie, string) {
	t.Helper()
	setupReq := httptest.NewRequest(http.MethodPost, "/api/v1/setup", bytes.NewBufferString(`{"username":"admin","password":"correct horse battery"}`))
	setupReq.Header.Set("Content-Type", "application/json")
	setupRec := httptest.NewRecorder()
	handler.ServeHTTP(setupRec, setupReq)
	if setupRec.Code != http.StatusOK {
		t.Fatalf("setup status = %d, want %d, body: %s", setupRec.Code, http.StatusOK, setupRec.Body.String())
	}
	var body struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.Unmarshal(setupRec.Body.Bytes(), &body); err != nil {
		t.Fatalf("setup response is not JSON: %v", err)
	}
	return setupRec.Result().Cookies(), body.CSRFToken
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

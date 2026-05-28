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
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"olcpanel/internal/auth"
	"olcpanel/internal/clients"
	"olcpanel/internal/config"
	"olcpanel/internal/server"
	"olcpanel/internal/storage"
	"olcpanel/internal/supervisor"
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

func TestSetupRejectsTrailingJSON(t *testing.T) {
	handler := server.New(config.Config{BindAddress: "127.0.0.1:8888"}, testAssets(), server.WithDatabase(testDB(t)))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup", bytes.NewBufferString(`{"username":"admin","password":"correct horse battery"} {}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body: %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestSetupRejectsOversizedJSON(t *testing.T) {
	handler := server.New(config.Config{BindAddress: "127.0.0.1:8888"}, testAssets(), server.WithDatabase(testDB(t)))
	body := `{"username":"admin","password":"` + strings.Repeat("x", 1<<20) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d, body: %s", rec.Code, http.StatusRequestEntityTooLarge, rec.Body.String())
	}
}

func TestSetupRejectsEmptyJSON(t *testing.T) {
	handler := server.New(config.Config{BindAddress: "127.0.0.1:8888"}, testAssets(), server.WithDatabase(testDB(t)))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup", bytes.NewReader(nil))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
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

func TestReloadEndpointRequiresSetupAuthAndSessionCSRF(t *testing.T) {
	db := testDB(t)
	reloader := &fakeReloader{}
	handler := server.New(config.Config{BindAddress: "127.0.0.1:8888"}, testAssets(), server.WithDatabase(db), server.WithSupervisor(reloader))

	beforeReq := httptest.NewRequest(http.MethodPost, "/api/v1/reload", nil)
	beforeRec := httptest.NewRecorder()
	handler.ServeHTTP(beforeRec, beforeReq)
	if beforeRec.Code != http.StatusForbidden {
		t.Fatalf("before setup status = %d, want %d", beforeRec.Code, http.StatusForbidden)
	}

	if _, err := auth.CreateFirstAdmin(context.Background(), db, "admin", "correct horse battery"); err != nil {
		t.Fatalf("CreateFirstAdmin returned error: %v", err)
	}
	unauthReq := httptest.NewRequest(http.MethodPost, "/api/v1/reload", nil)
	unauthRec := httptest.NewRecorder()
	handler.ServeHTTP(unauthRec, unauthReq)
	if unauthRec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want %d", unauthRec.Code, http.StatusUnauthorized)
	}

	loginHandler := server.New(config.Config{BindAddress: "127.0.0.1:8888"}, testAssets(), server.WithDatabase(testDB(t)), server.WithSupervisor(reloader))
	cookies, csrf := loginSession(t, loginHandler)
	withoutReq := httptest.NewRequest(http.MethodPost, "/api/v1/reload", nil)
	for _, cookie := range cookies {
		withoutReq.AddCookie(cookie)
	}
	withoutRec := httptest.NewRecorder()
	loginHandler.ServeHTTP(withoutRec, withoutReq)
	if withoutRec.Code != http.StatusForbidden {
		t.Fatalf("without csrf status = %d, want %d", withoutRec.Code, http.StatusForbidden)
	}
	if reloader.calls != 0 {
		t.Fatalf("reloader calls = %d, want 0 before csrf passes", reloader.calls)
	}

	withReq := httptest.NewRequest(http.MethodPost, "/api/v1/reload", nil)
	withReq.Header.Set("X-CSRF-Token", csrf)
	for _, cookie := range cookies {
		withReq.AddCookie(cookie)
	}
	withRec := httptest.NewRecorder()
	loginHandler.ServeHTTP(withRec, withReq)
	if withRec.Code != http.StatusOK {
		t.Fatalf("with csrf status = %d, want %d, body: %s", withRec.Code, http.StatusOK, withRec.Body.String())
	}
	if reloader.calls != 1 {
		t.Fatalf("reloader calls = %d, want 1", reloader.calls)
	}
}

func TestAPIKeyCanCallReloadWithoutCSRF(t *testing.T) {
	db := testDB(t)
	reloader := &fakeReloader{result: supervisor.ReloadResult{
		Summary: supervisor.Summary{Started: 1},
		Actions: []supervisor.ActionResult{{
			LocationID: "loc_1",
			ClientID:   "cl_1",
			Action:     supervisor.ActionStarted,
			Reason:     supervisor.ReasonNew,
		}},
	}}
	handler := server.New(config.Config{BindAddress: "127.0.0.1:8888"}, testAssets(), server.WithDatabase(db), server.WithSupervisor(reloader))
	token := createAPIKey(t, handler)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/reload", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if reloader.calls != 1 {
		t.Fatalf("reloader calls = %d, want 1", reloader.calls)
	}
	var body supervisor.ReloadResult
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if body.Summary.Started != 1 || len(body.Actions) != 1 || body.Actions[0].Action != supervisor.ActionStarted || body.Actions[0].Reason != supervisor.ReasonNew {
		t.Fatalf("reload response = %#v, want injected result", body)
	}
}

func TestDeleteAPIKeyReturnsNotFoundForMissingKey(t *testing.T) {
	db := testDB(t)
	handler := server.New(config.Config{BindAddress: "127.0.0.1:8888"}, testAssets(), server.WithDatabase(db))
	cookies, csrf := loginSession(t, handler)

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/api-keys/999", nil)
	deleteReq.Header.Set("X-CSRF-Token", csrf)
	for _, cookie := range cookies {
		deleteReq.AddCookie(cookie)
	}
	deleteRec := httptest.NewRecorder()
	handler.ServeHTTP(deleteRec, deleteReq)

	if deleteRec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d, body: %s", deleteRec.Code, http.StatusNotFound, deleteRec.Body.String())
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

func TestLoginRateLimitIgnoresForwardedFor(t *testing.T) {
	db := testDB(t)
	handler := server.New(config.Config{BindAddress: "127.0.0.1:8888"}, testAssets(), server.WithDatabase(db))
	if _, err := auth.CreateFirstAdmin(context.Background(), db, "admin", "correct horse battery"); err != nil {
		t.Fatalf("CreateFirstAdmin returned error: %v", err)
	}

	var rec *httptest.ResponseRecorder
	for i := 0; i < 6; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/login", bytes.NewBufferString(`{"username":"admin","password":"wrong horse battery"}`))
		req.RemoteAddr = "198.51.100.42:1234"
		req.Header.Set("X-Forwarded-For", "203.0.113."+strconv.Itoa(i))
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status after spoofed forwarded-for logins = %d, want %d", rec.Code, http.StatusTooManyRequests)
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

func TestClientsRoutesRequireSetupAndAuthentication(t *testing.T) {
	db := testDB(t)
	handler := server.New(config.Config{BindAddress: "127.0.0.1:8888"}, testAssets(), server.WithDatabase(db))

	beforeReq := httptest.NewRequest(http.MethodGet, "/api/v1/clients", nil)
	beforeRec := httptest.NewRecorder()
	handler.ServeHTTP(beforeRec, beforeReq)
	if beforeRec.Code != http.StatusForbidden {
		t.Fatalf("before setup status = %d, want %d", beforeRec.Code, http.StatusForbidden)
	}

	if _, err := auth.CreateFirstAdmin(context.Background(), db, "admin", "correct horse battery"); err != nil {
		t.Fatalf("CreateFirstAdmin returned error: %v", err)
	}
	afterReq := httptest.NewRequest(http.MethodGet, "/api/v1/clients", nil)
	afterRec := httptest.NewRecorder()
	handler.ServeHTTP(afterRec, afterReq)
	if afterRec.Code != http.StatusUnauthorized {
		t.Fatalf("after setup unauthenticated status = %d, want %d", afterRec.Code, http.StatusUnauthorized)
	}
}

func TestSessionClientMutationsRequireCSRF(t *testing.T) {
	db := testDB(t)
	handler := server.New(config.Config{BindAddress: "127.0.0.1:8888"}, testAssets(), server.WithDatabase(db))
	cookies, csrf := loginSession(t, handler)

	body := `{"name":"Client"}`
	withoutReq := httptest.NewRequest(http.MethodPost, "/api/v1/clients", bytes.NewBufferString(body))
	for _, cookie := range cookies {
		withoutReq.AddCookie(cookie)
	}
	withoutRec := httptest.NewRecorder()
	handler.ServeHTTP(withoutRec, withoutReq)
	if withoutRec.Code != http.StatusForbidden {
		t.Fatalf("without csrf status = %d, want %d", withoutRec.Code, http.StatusForbidden)
	}

	withReq := httptest.NewRequest(http.MethodPost, "/api/v1/clients", bytes.NewBufferString(body))
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

func TestAPIKeyCanManageClientsAndLocationsWithoutCSRF(t *testing.T) {
	db := testDB(t)
	handler := server.New(config.Config{BindAddress: "127.0.0.1:8888"}, testAssets(), server.WithDatabase(db))
	token := createAPIKey(t, handler)

	createClientReq := httptest.NewRequest(http.MethodPost, "/api/v1/clients", bytes.NewBufferString(`{"name":"Client","quota_bytes":2048}`))
	createClientReq.Header.Set("Authorization", "Bearer "+token)
	createClientRec := httptest.NewRecorder()
	handler.ServeHTTP(createClientRec, createClientReq)
	if createClientRec.Code != http.StatusOK {
		t.Fatalf("create client status = %d, want %d, body: %s", createClientRec.Code, http.StatusOK, createClientRec.Body.String())
	}
	var client struct {
		ID             string `json:"id"`
		Name           string `json:"name"`
		Enabled        bool   `json:"enabled"`
		QuotaState     string `json:"quota_state"`
		LocationsCount int    `json:"locations_count"`
	}
	if err := json.Unmarshal(createClientRec.Body.Bytes(), &client); err != nil {
		t.Fatalf("client response is not JSON: %v", err)
	}
	if client.ID == "" || client.Name != "Client" || !client.Enabled || client.QuotaState != "within_limit" {
		t.Fatalf("client = %#v, want created client defaults", client)
	}

	createLocationReq := httptest.NewRequest(http.MethodPost, "/api/v1/clients/"+client.ID+"/locations", bytes.NewBufferString(`{"name":"Main","provider":"wbstream","transport":"datachannel"}`))
	createLocationReq.Header.Set("Authorization", "Bearer "+token)
	createLocationRec := httptest.NewRecorder()
	handler.ServeHTTP(createLocationRec, createLocationReq)
	if createLocationRec.Code != http.StatusOK {
		t.Fatalf("create location status = %d, want %d, body: %s", createLocationRec.Code, http.StatusOK, createLocationRec.Body.String())
	}
	var location struct {
		ID                 string          `json:"id"`
		Provider           string          `json:"provider"`
		Transport          string          `json:"transport"`
		TransportStability string          `json:"transport_stability"`
		RoomID             string          `json:"room_id"`
		CryptoKey          string          `json:"crypto_key"`
		TransportPayload   json.RawMessage `json:"transport_payload"`
		DNS                string          `json:"dns"`
	}
	if err := json.Unmarshal(createLocationRec.Body.Bytes(), &location); err != nil {
		t.Fatalf("location response is not JSON: %v", err)
	}
	if location.ID == "" || location.Provider != "wbstream" || location.Transport != "datachannel" || location.TransportStability != "stable" || location.RoomID == "" || len(location.CryptoKey) != 64 || string(location.TransportPayload) != `{}` || location.DNS != "8.8.8.8:53" {
		t.Fatalf("location = %#v, want generated location defaults", location)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/clients", nil)
	listReq.Header.Set("Authorization", "Bearer "+token)
	listRec := httptest.NewRecorder()
	handler.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d", listRec.Code, http.StatusOK)
	}
	var clients []struct {
		ID             string `json:"id"`
		LocationsCount int    `json:"locations_count"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &clients); err != nil {
		t.Fatalf("list response is not JSON: %v", err)
	}
	if len(clients) != 1 || clients[0].ID != client.ID || clients[0].LocationsCount != 1 {
		t.Fatalf("clients = %#v, want one client with one location", clients)
	}
}

func TestClientLocationValidationDeleteCascadeAndRotate(t *testing.T) {
	db := testDB(t)
	handler := server.New(config.Config{BindAddress: "127.0.0.1:8888"}, testAssets(), server.WithDatabase(db))
	cookies, csrf := loginSession(t, handler)

	clientID := createClientViaSession(t, handler, cookies, csrf, `{"name":"Client"}`)

	invalidReq := httptest.NewRequest(http.MethodPost, "/api/v1/clients/"+clientID+"/locations", bytes.NewBufferString(`{"name":"Bad","provider":"telemost","transport":"datachannel"}`))
	invalidReq.Header.Set("X-CSRF-Token", csrf)
	for _, cookie := range cookies {
		invalidReq.AddCookie(cookie)
	}
	invalidRec := httptest.NewRecorder()
	handler.ServeHTTP(invalidRec, invalidReq)
	if invalidRec.Code != http.StatusBadRequest {
		t.Fatalf("invalid location status = %d, want %d", invalidRec.Code, http.StatusBadRequest)
	}

	createLocationReq := httptest.NewRequest(http.MethodPost, "/api/v1/clients/"+clientID+"/locations", bytes.NewBufferString(`{"name":"Main","provider":"jitsi","transport":"datachannel"}`))
	createLocationReq.Header.Set("X-CSRF-Token", csrf)
	for _, cookie := range cookies {
		createLocationReq.AddCookie(cookie)
	}
	createLocationRec := httptest.NewRecorder()
	handler.ServeHTTP(createLocationRec, createLocationReq)
	if createLocationRec.Code != http.StatusOK {
		t.Fatalf("create location status = %d, want %d, body: %s", createLocationRec.Code, http.StatusOK, createLocationRec.Body.String())
	}
	var original struct {
		ID                 string `json:"id"`
		TransportStability string `json:"transport_stability"`
		RoomID             string `json:"room_id"`
		CryptoKey          string `json:"crypto_key"`
	}
	if err := json.Unmarshal(createLocationRec.Body.Bytes(), &original); err != nil {
		t.Fatalf("location response is not JSON: %v", err)
	}
	if original.TransportStability != "unstable" {
		t.Fatalf("stability = %q, want unstable", original.TransportStability)
	}

	rotateKeysReq := httptest.NewRequest(http.MethodPost, "/api/v1/clients/"+clientID+"/rotate", bytes.NewBufferString(`{}`))
	rotateKeysReq.Header.Set("X-CSRF-Token", csrf)
	for _, cookie := range cookies {
		rotateKeysReq.AddCookie(cookie)
	}
	rotateKeysRec := httptest.NewRecorder()
	handler.ServeHTTP(rotateKeysRec, rotateKeysReq)
	if rotateKeysRec.Code != http.StatusOK {
		t.Fatalf("rotate keys status = %d, want %d, body: %s", rotateKeysRec.Code, http.StatusOK, rotateKeysRec.Body.String())
	}
	var keyRotation []struct {
		RoomID    string `json:"room_id"`
		CryptoKey string `json:"crypto_key"`
	}
	if err := json.Unmarshal(rotateKeysRec.Body.Bytes(), &keyRotation); err != nil {
		t.Fatalf("rotate keys response is not JSON: %v", err)
	}
	if keyRotation[0].CryptoKey == original.CryptoKey || keyRotation[0].RoomID != original.RoomID {
		t.Fatalf("key rotation = %#v, want changed key and same room", keyRotation)
	}

	rotateRoomsReq := httptest.NewRequest(http.MethodPost, "/api/v1/clients/"+clientID+"/rotate", bytes.NewBufferString(`{"rotate_rooms":true}`))
	rotateRoomsReq.Header.Set("X-CSRF-Token", csrf)
	for _, cookie := range cookies {
		rotateRoomsReq.AddCookie(cookie)
	}
	rotateRoomsRec := httptest.NewRecorder()
	handler.ServeHTTP(rotateRoomsRec, rotateRoomsReq)
	if rotateRoomsRec.Code != http.StatusOK {
		t.Fatalf("rotate rooms status = %d, want %d, body: %s", rotateRoomsRec.Code, http.StatusOK, rotateRoomsRec.Body.String())
	}
	var roomRotation []struct {
		RoomID    string `json:"room_id"`
		CryptoKey string `json:"crypto_key"`
	}
	if err := json.Unmarshal(rotateRoomsRec.Body.Bytes(), &roomRotation); err != nil {
		t.Fatalf("rotate rooms response is not JSON: %v", err)
	}
	if roomRotation[0].CryptoKey == keyRotation[0].CryptoKey || roomRotation[0].RoomID == keyRotation[0].RoomID {
		t.Fatalf("room rotation = %#v, want changed key and changed room", roomRotation)
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/clients/"+clientID, nil)
	deleteReq.Header.Set("X-CSRF-Token", csrf)
	for _, cookie := range cookies {
		deleteReq.AddCookie(cookie)
	}
	deleteRec := httptest.NewRecorder()
	handler.ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusNoContent {
		t.Fatalf("delete client status = %d, want %d, body: %s", deleteRec.Code, http.StatusNoContent, deleteRec.Body.String())
	}

	listLocationsReq := httptest.NewRequest(http.MethodGet, "/api/v1/clients/"+clientID+"/locations", nil)
	for _, cookie := range cookies {
		listLocationsReq.AddCookie(cookie)
	}
	listLocationsRec := httptest.NewRecorder()
	handler.ServeHTTP(listLocationsRec, listLocationsReq)
	if listLocationsRec.Code != http.StatusNotFound {
		t.Fatalf("locations after client delete status = %d, want %d", listLocationsRec.Code, http.StatusNotFound)
	}
}

func TestSubscriptionTokenEndpointServesPlaintextWithoutAdminAuth(t *testing.T) {
	db := testDB(t)
	handler := server.New(config.Config{BindAddress: "127.0.0.1:8888"}, testAssets(), server.WithDatabase(db))
	client := seedSubscriptionClient(t, db, true, true)

	req := httptest.NewRequest(http.MethodGet, "/sub/"+client.SubscriptionToken, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want text/plain; charset=utf-8", got)
	}
	if body := rec.Body.String(); !strings.Contains(body, "#name: Client") || !strings.Contains(body, "olcrtc://wbstream?datachannel@") {
		t.Fatalf("subscription body = %q, want metadata and olcrtc URI", body)
	}
}

func TestSubscriptionTokenRotationInvalidatesOldToken(t *testing.T) {
	db := testDB(t)
	handler := server.New(config.Config{BindAddress: "127.0.0.1:8888"}, testAssets(), server.WithDatabase(db))
	cookies, csrf := loginSession(t, handler)
	client := seedSubscriptionClient(t, db, true, true)
	oldToken := client.SubscriptionToken

	rotateReq := httptest.NewRequest(http.MethodPost, "/api/v1/clients/"+client.ID+"/rotate", bytes.NewBufferString(`{"rotate_subscription_token":true}`))
	rotateReq.Header.Set("X-CSRF-Token", csrf)
	for _, cookie := range cookies {
		rotateReq.AddCookie(cookie)
	}
	rotateRec := httptest.NewRecorder()
	handler.ServeHTTP(rotateRec, rotateReq)
	if rotateRec.Code != http.StatusOK {
		t.Fatalf("rotate status = %d, want %d, body: %s", rotateRec.Code, http.StatusOK, rotateRec.Body.String())
	}

	oldReq := httptest.NewRequest(http.MethodGet, "/sub/"+oldToken, nil)
	oldRec := httptest.NewRecorder()
	handler.ServeHTTP(oldRec, oldReq)
	if oldRec.Code != http.StatusNotFound {
		t.Fatalf("old token status = %d, want %d", oldRec.Code, http.StatusNotFound)
	}
	rotated, err := clients.GetClient(context.Background(), db, client.ID)
	if err != nil {
		t.Fatalf("GetClient returned error: %v", err)
	}
	newReq := httptest.NewRequest(http.MethodGet, "/sub/"+rotated.SubscriptionToken, nil)
	newRec := httptest.NewRecorder()
	handler.ServeHTTP(newRec, newReq)
	if newRec.Code != http.StatusOK {
		t.Fatalf("new token status = %d, want %d, body: %s", newRec.Code, http.StatusOK, newRec.Body.String())
	}
}

func TestPublicClientEndpointIsOptIn(t *testing.T) {
	db := testDB(t)
	handler := server.New(config.Config{BindAddress: "127.0.0.1:8888"}, testAssets(), server.WithDatabase(db))
	client := seedSubscriptionClient(t, db, true, true)

	disabledReq := httptest.NewRequest(http.MethodGet, "/c/"+client.ID, nil)
	disabledRec := httptest.NewRecorder()
	handler.ServeHTTP(disabledRec, disabledReq)
	if disabledRec.Code != http.StatusNotFound {
		t.Fatalf("disabled public client status = %d, want %d", disabledRec.Code, http.StatusNotFound)
	}

	if err := storage.PutSettings(context.Background(), db, storage.Settings{
		UILocale:                    "en",
		PublicClientEndpointEnabled: true,
		BackupPath:                  "/var/lib/olcpanel/backups",
		QuotaLockMode:               "stop",
	}); err != nil {
		t.Fatalf("PutSettings returned error: %v", err)
	}

	enabledReq := httptest.NewRequest(http.MethodGet, "/c/"+client.ID, nil)
	enabledRec := httptest.NewRecorder()
	handler.ServeHTTP(enabledRec, enabledReq)
	if enabledRec.Code != http.StatusOK {
		t.Fatalf("enabled public client status = %d, want %d, body: %s", enabledRec.Code, http.StatusOK, enabledRec.Body.String())
	}
	if !strings.Contains(enabledRec.Body.String(), "#name: Client") {
		t.Fatalf("body = %q, want subscription plaintext", enabledRec.Body.String())
	}
}

func TestSubscriptionEndpointsReturnNotFoundForInvalidOrUnavailableClients(t *testing.T) {
	for name, tc := range map[string]struct {
		clientEnabled   bool
		locationEnabled bool
		path            func(clients.Client) string
	}{
		"unknown token": {clientEnabled: true, locationEnabled: true, path: func(clients.Client) string { return "/sub/sub_missing" }},
		"disabled client": {clientEnabled: false, locationEnabled: true, path: func(client clients.Client) string {
			return "/sub/" + client.SubscriptionToken
		}},
		"no enabled locations": {clientEnabled: true, locationEnabled: false, path: func(client clients.Client) string {
			return "/sub/" + client.SubscriptionToken
		}},
	} {
		t.Run(name, func(t *testing.T) {
			db := testDB(t)
			handler := server.New(config.Config{BindAddress: "127.0.0.1:8888"}, testAssets(), server.WithDatabase(db))
			client := seedSubscriptionClient(t, db, tc.clientEnabled, tc.locationEnabled)

			req := httptest.NewRequest(http.MethodGet, tc.path(client), nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want %d, body: %s", rec.Code, http.StatusNotFound, rec.Body.String())
			}
		})
	}
}

type fakeReloader struct {
	calls  int
	result supervisor.ReloadResult
	err    error
}

func (reloader *fakeReloader) Reload(context.Context) (supervisor.ReloadResult, error) {
	reloader.calls++
	return reloader.result, reloader.err
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

func createAPIKey(t *testing.T, handler http.Handler) string {
	t.Helper()
	cookies, csrf := loginSession(t, handler)
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/api-keys", bytes.NewBufferString(`{"name":"automation"}`))
	createReq.Header.Set("X-CSRF-Token", csrf)
	for _, cookie := range cookies {
		createReq.AddCookie(cookie)
	}
	createRec := httptest.NewRecorder()
	handler.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusOK {
		t.Fatalf("create key status = %d, want %d, body: %s", createRec.Code, http.StatusOK, createRec.Body.String())
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(createRec.Body.Bytes(), &body); err != nil {
		t.Fatalf("create key response is not JSON: %v", err)
	}
	return body.Token
}

func createClientViaSession(t *testing.T, handler http.Handler, cookies []*http.Cookie, csrf string, body string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/clients", bytes.NewBufferString(body))
	req.Header.Set("X-CSRF-Token", csrf)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create client status = %d, want %d, body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("client response is not JSON: %v", err)
	}
	return created.ID
}

func seedSubscriptionClient(t *testing.T, db *sql.DB, clientEnabled, locationEnabled bool) clients.Client {
	t.Helper()
	ctx := context.Background()
	client, err := clients.CreateClient(ctx, db, clients.ClientInput{
		Name:    "Client",
		Enabled: &clientEnabled,
	})
	if err != nil {
		t.Fatalf("CreateClient returned error: %v", err)
	}
	if _, err := clients.CreateLocation(ctx, db, client.ID, clients.LocationInput{
		Name:      "Main",
		Enabled:   &locationEnabled,
		Provider:  "wbstream",
		Transport: "datachannel",
	}); err != nil {
		t.Fatalf("CreateLocation returned error: %v", err)
	}
	return client
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

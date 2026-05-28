package server_test

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"olcpanel/internal/config"
	"olcpanel/internal/server"
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

func testAssets() fs.FS {
	return fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<!doctype html><title>OlcRTC Panel</title>")},
	}
}

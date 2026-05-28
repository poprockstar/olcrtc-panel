package server

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"olcpanel/internal/config"
)

type StateResponse struct {
	Service       string `json:"service"`
	APIVersion    string `json:"api_version"`
	SetupRequired bool   `json:"setup_required"`
	BindAddress   string `json:"bind_address"`
}

func New(cfg config.Config, assets fs.FS) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v1/state", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(StateResponse{
			Service:       "olcpanel",
			APIVersion:    "v1",
			SetupRequired: true,
			BindAddress:   cfg.BindAddress,
		})
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}

		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}

		if data, err := fs.ReadFile(assets, path); err == nil {
			http.ServeContent(w, r, path, time.Time{}, bytes.NewReader(data))
			return
		}

		data, err := fs.ReadFile(assets, "index.html")
		if err != nil {
			http.Error(w, "embedded UI is unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(data)
	})

	return mux
}

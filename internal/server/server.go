package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"olcpanel/internal/auth"
	"olcpanel/internal/backup"
	"olcpanel/internal/clients"
	"olcpanel/internal/config"
	"olcpanel/internal/metrics"
	"olcpanel/internal/observability"
	"olcpanel/internal/storage"
	"olcpanel/internal/subscriptions"
	"olcpanel/internal/supervisor"
)

const maxJSONBodyBytes = 1 << 20

type StateResponse struct {
	Service       string `json:"service"`
	APIVersion    string `json:"api_version"`
	SetupRequired bool   `json:"setup_required"`
	BindAddress   string `json:"bind_address"`
	BasePath      string `json:"base_path"`
	Authenticated bool   `json:"authenticated,omitempty"`
}

type Option func(*dependencies)

type dependencies struct {
	db                *sql.DB
	supervisor        reloader
	statuses          metrics.StatusProvider
	logStore          logStore
	hostReader        metrics.HostReader
	startedAt         time.Time
	setupLimiter      *auth.RateLimiter
	loginLimiter      *auth.RateLimiter
	apiKeyFailLimiter *auth.RateLimiter
	basePath          string
}

type reloader interface {
	Reload(context.Context) (supervisor.ReloadResult, error)
}

type logStore interface {
	Query(context.Context, observability.LogQuery) ([]observability.LogEntry, error)
}

func WithDatabase(db *sql.DB) Option {
	return func(deps *dependencies) {
		deps.db = db
	}
}

func WithSupervisor(supervisor reloader) Option {
	return func(deps *dependencies) {
		deps.supervisor = supervisor
		if statuses, ok := supervisor.(metrics.StatusProvider); ok {
			deps.statuses = statuses
		}
	}
}

func WithLogStore(store logStore) Option {
	return func(deps *dependencies) {
		deps.logStore = store
	}
}

func WithMetricsHostReader(reader metrics.HostReader) Option {
	return func(deps *dependencies) {
		deps.hostReader = reader
	}
}

func New(cfg config.Config, assets fs.FS, options ...Option) http.Handler {
	deps := dependencies{
		startedAt:         time.Now().UTC(),
		setupLimiter:      auth.NewRateLimiter(5, time.Minute),
		loginLimiter:      auth.NewRateLimiter(5, time.Minute),
		apiKeyFailLimiter: auth.NewRateLimiter(10, time.Minute),
		basePath:          cfg.BasePath,
	}
	for _, option := range options {
		option(&deps)
	}

	appMux := http.NewServeMux()
	registerStateRoutes(appMux, cfg, deps)
	registerAuthRoutes(appMux, deps)
	registerSettingsRoutes(appMux, deps)
	registerReloadRoutes(appMux, deps)
	registerClientRoutes(appMux, deps)
	registerSubscriptionRoutes(appMux, deps)
	registerAPIKeyRoutes(appMux, deps)
	registerObservabilityRoutes(appMux, deps)
	registerBackupRoutes(appMux, cfg, deps)
	registerStaticRoutes(appMux, cfg, assets)

	return withBasePath(cfg.BasePath, appMux)
}

func withBasePath(basePath string, handler http.Handler) http.Handler {
	if basePath == "" {
		return handler
	}
	stripped := http.StripPrefix(basePath, handler)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == basePath:
			http.Redirect(w, r, basePath+"/", http.StatusPermanentRedirect)
		case r.URL.Path == "/":
			http.Redirect(w, r, basePath+"/", http.StatusTemporaryRedirect)
		case strings.HasPrefix(r.URL.Path, basePath+"/"):
			stripped.ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}
	})
}

type restoreRuntime interface {
	StopAll(context.Context) error
	Reload(context.Context) (supervisor.ReloadResult, error)
}

func registerBackupRoutes(mux *http.ServeMux, cfg config.Config, deps dependencies) {
	mux.HandleFunc("GET /api/v1/backups", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := deps.requireAdmin(w, r, false); !ok {
			return
		}
		records, err := backup.List(r.Context(), deps.db)
		if err != nil {
			http.Error(w, "failed to list backups", http.StatusInternalServerError)
			return
		}
		writeJSON(w, records)
	})

	mux.HandleFunc("POST /api/v1/backup", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := deps.requireAdmin(w, r, true); !ok {
			return
		}
		settings, err := storage.GetSettings(r.Context(), deps.db)
		if err != nil {
			http.Error(w, "failed to read settings", http.StatusInternalServerError)
			return
		}
		record, err := backup.Create(r.Context(), deps.db, backup.CreateOptions{
			DatabaseURL: cfg.DatabaseURL,
			OutputPath:  settings.BackupPath,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, record)
	})

	mux.HandleFunc("POST /api/v1/restore", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := deps.requireAdmin(w, r, true); !ok {
			return
		}
		runtime, ok := deps.supervisor.(restoreRuntime)
		if !ok {
			http.Error(w, "restore runtime is unavailable", http.StatusServiceUnavailable)
			return
		}
		var payload restoreRequest
		if !decodeJSON(w, r, &payload) {
			return
		}
		result, err := backup.RestoreKnown(r.Context(), deps.db, payload.BackupID, runtime)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, result)
	})

	mux.HandleFunc("GET /api/v1/export", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := deps.requireAdmin(w, r, false); !ok {
			return
		}
		doc, err := backup.ExportPanel(r.Context(), deps.db)
		if err != nil {
			http.Error(w, "failed to export panel JSON", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Disposition", `attachment; filename="`+backup.DefaultExportFilename()+`"`)
		writeJSON(w, doc)
	})

	mux.HandleFunc("POST /api/v1/import", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := deps.requireAdmin(w, r, true); !ok {
			return
		}
		var doc backup.PanelJSON
		if !decodeJSON(w, r, &doc) {
			return
		}
		result, err := backup.ImportPanel(r.Context(), deps.db, doc, backup.ImportOptions{
			ApplySettings: r.URL.Query().Get("apply_settings") == "true",
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, result)
	})
}

func registerReloadRoutes(mux *http.ServeMux, deps dependencies) {
	mux.HandleFunc("POST /api/v1/reload", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := deps.requireAdmin(w, r, true); !ok {
			return
		}
		if deps.supervisor == nil {
			http.Error(w, "supervisor is unavailable", http.StatusServiceUnavailable)
			return
		}
		result, err := deps.supervisor.Reload(r.Context())
		if err != nil {
			http.Error(w, "failed to reload supervisor", http.StatusInternalServerError)
			return
		}
		writeJSON(w, result)
	})
}

func registerStateRoutes(mux *http.ServeMux, cfg config.Config, deps dependencies) {
	mux.HandleFunc("GET /api/v1/state", func(w http.ResponseWriter, r *http.Request) {
		setupRequired := true
		authenticated := false
		if deps.db != nil {
			usersExist, err := auth.UsersExist(r.Context(), deps.db)
			if err != nil {
				http.Error(w, "failed to read setup state", http.StatusInternalServerError)
				return
			}
			setupRequired = !usersExist
			_, authenticated = deps.authenticateOptional(r)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(StateResponse{
			Service:       "olcpanel",
			APIVersion:    "v1",
			SetupRequired: setupRequired,
			BindAddress:   cfg.BindAddress,
			BasePath:      cfg.BasePath,
			Authenticated: authenticated,
		})
	})
}

func registerAuthRoutes(mux *http.ServeMux, deps dependencies) {
	mux.HandleFunc("POST /api/v1/setup", func(w http.ResponseWriter, r *http.Request) {
		if deps.db == nil {
			http.Error(w, "database is unavailable", http.StatusServiceUnavailable)
			return
		}
		if !sameOrigin(r) {
			http.Error(w, "cross-origin request rejected", http.StatusForbidden)
			return
		}
		if !deps.setupLimiter.Allow(clientKey(r, "setup")) {
			http.Error(w, "too many setup attempts", http.StatusTooManyRequests)
			return
		}
		var payload credentialsRequest
		if !decodeJSON(w, r, &payload) {
			return
		}
		user, err := auth.CreateFirstAdmin(r.Context(), deps.db, payload.Username, payload.Password)
		if errors.Is(err, auth.ErrSetupComplete) {
			http.Error(w, "setup is already complete", http.StatusConflict)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		session, err := auth.CreateSession(r.Context(), deps.db, user.ID, 24*time.Hour)
		if err != nil {
			http.Error(w, "failed to create session", http.StatusInternalServerError)
			return
		}
		setSessionCookie(w, r, deps.cookiePath(), session.ID, session.ExpiresAt)
		writeJSON(w, sessionResponse{Username: user.Username, CSRFToken: session.CSRFToken})
	})

	mux.HandleFunc("POST /api/v1/login", func(w http.ResponseWriter, r *http.Request) {
		if deps.db == nil {
			http.Error(w, "database is unavailable", http.StatusServiceUnavailable)
			return
		}
		if !sameOrigin(r) {
			http.Error(w, "cross-origin request rejected", http.StatusForbidden)
			return
		}
		usersExist, err := auth.UsersExist(r.Context(), deps.db)
		if err != nil {
			http.Error(w, "failed to read setup state", http.StatusInternalServerError)
			return
		}
		if !usersExist {
			http.Error(w, "setup required", http.StatusForbidden)
			return
		}
		if !deps.loginLimiter.Allow(clientKey(r, "login")) {
			http.Error(w, "too many login attempts", http.StatusTooManyRequests)
			return
		}
		var payload credentialsRequest
		if !decodeJSON(w, r, &payload) {
			return
		}
		user, err := auth.VerifyLogin(r.Context(), deps.db, payload.Username, payload.Password)
		if err != nil {
			http.Error(w, "invalid username or password", http.StatusUnauthorized)
			return
		}
		session, err := auth.CreateSession(r.Context(), deps.db, user.ID, 24*time.Hour)
		if err != nil {
			http.Error(w, "failed to create session", http.StatusInternalServerError)
			return
		}
		setSessionCookie(w, r, deps.cookiePath(), session.ID, session.ExpiresAt)
		writeJSON(w, sessionResponse{Username: user.Username, CSRFToken: session.CSRFToken})
	})

	mux.HandleFunc("POST /api/v1/logout", func(w http.ResponseWriter, r *http.Request) {
		ctx, ok := deps.requireSessionAdmin(w, r, true)
		if !ok {
			return
		}
		if err := auth.RevokeSession(r.Context(), deps.db, ctx.session.ID); err != nil {
			http.Error(w, "failed to revoke session", http.StatusInternalServerError)
			return
		}
		clearSessionCookie(w, r, deps.cookiePath())
		w.WriteHeader(http.StatusNoContent)
	})
}

func registerSettingsRoutes(mux *http.ServeMux, deps dependencies) {
	mux.HandleFunc("GET /api/v1/settings", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := deps.requireAdmin(w, r, false); !ok {
			return
		}

		settings, err := storage.GetSettings(r.Context(), deps.db)
		if err != nil {
			http.Error(w, "failed to read settings", http.StatusInternalServerError)
			return
		}

		writeJSON(w, settings)
	})

	mux.HandleFunc("PUT /api/v1/settings", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := deps.requireAdmin(w, r, true); !ok {
			return
		}

		var settings storage.Settings
		if !decodeJSON(w, r, &settings) {
			return
		}
		if err := storage.ValidateSettings(settings); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := storage.PutSettings(r.Context(), deps.db, settings); err != nil {
			http.Error(w, "failed to persist settings", http.StatusInternalServerError)
			return
		}

		writeJSON(w, settings)
	})
}

func registerClientRoutes(mux *http.ServeMux, deps dependencies) {
	mux.HandleFunc("GET /api/v1/clients", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := deps.requireAdmin(w, r, false); !ok {
			return
		}
		result, err := clients.ListClients(r.Context(), deps.db)
		if err != nil {
			http.Error(w, "failed to list clients", http.StatusInternalServerError)
			return
		}
		writeJSON(w, result)
	})

	mux.HandleFunc("POST /api/v1/clients", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := deps.requireAdmin(w, r, true); !ok {
			return
		}
		var input clients.ClientInput
		if !decodeJSON(w, r, &input) {
			return
		}
		client, err := clients.CreateClient(r.Context(), deps.db, input)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, client)
	})

	mux.HandleFunc("GET /api/v1/clients/{id}", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := deps.requireAdmin(w, r, false); !ok {
			return
		}
		client, err := clients.GetClient(r.Context(), deps.db, r.PathValue("id"))
		if errors.Is(err, clients.ErrNotFound) {
			http.Error(w, "client not found", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, "failed to read client", http.StatusInternalServerError)
			return
		}
		writeJSON(w, client)
	})

	mux.HandleFunc("PUT /api/v1/clients/{id}", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := deps.requireAdmin(w, r, true); !ok {
			return
		}
		var input clients.ClientInput
		if !decodeJSON(w, r, &input) {
			return
		}
		client, err := clients.UpdateClient(r.Context(), deps.db, r.PathValue("id"), input)
		if errors.Is(err, clients.ErrNotFound) {
			http.Error(w, "client not found", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, client)
	})

	mux.HandleFunc("DELETE /api/v1/clients/{id}", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := deps.requireAdmin(w, r, true); !ok {
			return
		}
		err := clients.DeleteClient(r.Context(), deps.db, r.PathValue("id"))
		if errors.Is(err, clients.ErrNotFound) {
			http.Error(w, "client not found", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, "failed to delete client", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("GET /api/v1/clients/{id}/locations", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := deps.requireAdmin(w, r, false); !ok {
			return
		}
		result, err := clients.ListLocations(r.Context(), deps.db, r.PathValue("id"))
		if errors.Is(err, clients.ErrNotFound) {
			http.Error(w, "client not found", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, "failed to list locations", http.StatusInternalServerError)
			return
		}
		result = deps.overlayLocations(result)
		writeJSON(w, result)
	})

	mux.HandleFunc("POST /api/v1/clients/{id}/locations", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := deps.requireAdmin(w, r, true); !ok {
			return
		}
		input, ok := decodeLocationInput(w, r)
		if !ok {
			return
		}
		location, err := clients.CreateLocation(r.Context(), deps.db, r.PathValue("id"), input)
		if errors.Is(err, clients.ErrNotFound) {
			http.Error(w, "client not found", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		location = deps.overlayLocation(location)
		writeJSON(w, location)
	})

	mux.HandleFunc("PUT /api/v1/clients/{id}/locations/{location_id}", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := deps.requireAdmin(w, r, true); !ok {
			return
		}
		input, ok := decodeLocationInput(w, r)
		if !ok {
			return
		}
		location, err := clients.UpdateLocation(r.Context(), deps.db, r.PathValue("id"), r.PathValue("location_id"), input)
		if errors.Is(err, clients.ErrNotFound) {
			http.Error(w, "location not found", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		location = deps.overlayLocation(location)
		writeJSON(w, location)
	})

	mux.HandleFunc("DELETE /api/v1/clients/{id}/locations/{location_id}", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := deps.requireAdmin(w, r, true); !ok {
			return
		}
		err := clients.DeleteLocation(r.Context(), deps.db, r.PathValue("id"), r.PathValue("location_id"))
		if errors.Is(err, clients.ErrNotFound) {
			http.Error(w, "location not found", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, "failed to delete location", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("POST /api/v1/clients/{id}/rotate", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := deps.requireAdmin(w, r, true); !ok {
			return
		}
		var input rotateRequest
		if !decodeJSON(w, r, &input) {
			return
		}
		if input.RotateSubscriptionToken {
			if _, err := clients.RotateClientSubscriptionToken(r.Context(), deps.db, r.PathValue("id")); errors.Is(err, clients.ErrNotFound) {
				http.Error(w, "client not found", http.StatusNotFound)
				return
			} else if err != nil {
				http.Error(w, "failed to rotate subscription token", http.StatusInternalServerError)
				return
			}
		}
		var locations []clients.Location
		var err error
		if input.RotateSubscriptionToken && !input.RotateRooms {
			locations, err = clients.ListLocations(r.Context(), deps.db, r.PathValue("id"))
		} else {
			locations, err = clients.RotateClientLocations(r.Context(), deps.db, r.PathValue("id"), input.RotateRooms)
		}
		if errors.Is(err, clients.ErrNotFound) {
			http.Error(w, "client not found", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, "failed to rotate locations", http.StatusInternalServerError)
			return
		}
		locations = deps.overlayLocations(locations)
		writeJSON(w, locations)
	})
}

func registerSubscriptionRoutes(mux *http.ServeMux, deps dependencies) {
	mux.HandleFunc("GET /sub/{token}", func(w http.ResponseWriter, r *http.Request) {
		if deps.db == nil {
			http.Error(w, "database is unavailable", http.StatusServiceUnavailable)
			return
		}
		client, err := clients.GetClientBySubscriptionToken(r.Context(), deps.db, r.PathValue("token"))
		if errors.Is(err, clients.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, "failed to read subscription", http.StatusInternalServerError)
			return
		}
		writeSubscription(w, r, deps.db, client)
	})

	mux.HandleFunc("GET /c/{id}", func(w http.ResponseWriter, r *http.Request) {
		if deps.db == nil {
			http.Error(w, "database is unavailable", http.StatusServiceUnavailable)
			return
		}
		settings, err := storage.GetSettings(r.Context(), deps.db)
		if err != nil {
			http.Error(w, "failed to read settings", http.StatusInternalServerError)
			return
		}
		if !settings.PublicClientEndpointEnabled {
			http.NotFound(w, r)
			return
		}
		client, err := clients.GetClient(r.Context(), deps.db, r.PathValue("id"))
		if errors.Is(err, clients.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, "failed to read subscription", http.StatusInternalServerError)
			return
		}
		writeSubscription(w, r, deps.db, client)
	})
}

func writeSubscription(w http.ResponseWriter, r *http.Request, db *sql.DB, client clients.Client) {
	if !subscriptionAvailable(client) {
		http.NotFound(w, r)
		return
	}
	locations, err := clients.ListLocations(r.Context(), db, client.ID)
	if errors.Is(err, clients.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "failed to read subscription locations", http.StatusInternalServerError)
		return
	}
	body, err := subscriptions.Render(subscriptions.Snapshot{
		Client:    client,
		Locations: locations,
		UpdatedAt: time.Now().UTC(),
		Refresh:   "10m",
	})
	if errors.Is(err, subscriptions.ErrNoEnabledLocations) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "failed to render subscription", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(body))
}

func subscriptionAvailable(client clients.Client) bool {
	return client.Enabled && client.ExpiryState != clients.ExpiryExpired && client.QuotaState != clients.QuotaExceeded
}

func registerAPIKeyRoutes(mux *http.ServeMux, deps dependencies) {
	mux.HandleFunc("GET /api/v1/api-keys", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := deps.requireSessionAdmin(w, r, false); !ok {
			return
		}
		keys, err := auth.ListAPIKeys(r.Context(), deps.db)
		if err != nil {
			http.Error(w, "failed to list api keys", http.StatusInternalServerError)
			return
		}
		writeJSON(w, keys)
	})

	mux.HandleFunc("POST /api/v1/api-keys", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := deps.requireSessionAdmin(w, r, true); !ok {
			return
		}
		var payload apiKeyCreateRequest
		if !decodeJSON(w, r, &payload) {
			return
		}
		key, token, err := auth.CreateAPIKey(r.Context(), deps.db, payload.Name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, apiKeyCreateResponse{ID: key.ID, Name: key.Name, Token: token})
	})

	mux.HandleFunc("DELETE /api/v1/api-keys/{id}", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := deps.requireSessionAdmin(w, r, true); !ok {
			return
		}
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			http.Error(w, "invalid api key id", http.StatusBadRequest)
			return
		}
		if err := auth.RevokeAPIKey(r.Context(), deps.db, id); errors.Is(err, auth.ErrNotFound) {
			http.Error(w, "api key not found", http.StatusNotFound)
			return
		} else if err != nil {
			http.Error(w, "failed to revoke api key", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func registerObservabilityRoutes(mux *http.ServeMux, deps dependencies) {
	mux.HandleFunc("GET /api/v1/logs", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := deps.requireAdmin(w, r, false); !ok {
			return
		}
		if deps.logStore == nil {
			http.Error(w, "log sink is unavailable", http.StatusServiceUnavailable)
			return
		}
		query, err := parseLogQuery(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		entries, err := deps.logStore.Query(r.Context(), query)
		if errors.Is(err, observability.ErrUnavailable) {
			http.Error(w, "log sink is unavailable", http.StatusServiceUnavailable)
			return
		}
		if err != nil {
			http.Error(w, "failed to read logs", http.StatusInternalServerError)
			return
		}
		if r.URL.Query().Get("format") == "text" {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write([]byte(observability.FormatText(entries)))
			return
		}
		writeJSON(w, struct {
			Entries []observability.LogEntry `json:"entries"`
		}{Entries: entries})
	})

	mux.HandleFunc("GET /api/v1/metrics", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := deps.requireAdmin(w, r, false); !ok {
			return
		}
		snapshot, err := metrics.BuildSnapshot(r.Context(), deps.db, metrics.Options{
			StartedAt:  deps.startedAt,
			HostReader: deps.hostReader,
			Statuses:   deps.statuses,
		})
		if err != nil {
			http.Error(w, "failed to build metrics snapshot", http.StatusInternalServerError)
			return
		}
		writeJSON(w, snapshot)
	})
}

func registerStaticRoutes(mux *http.ServeMux, cfg config.Config, assets fs.FS) {
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
			if path == "index.html" {
				serveIndex(w, r, cfg, data)
				return
			}
			http.ServeContent(w, r, path, time.Time{}, bytes.NewReader(data))
			return
		}

		data, err := fs.ReadFile(assets, "index.html")
		if err != nil {
			http.Error(w, "embedded UI is unavailable", http.StatusInternalServerError)
			return
		}
		serveIndex(w, r, cfg, data)
	})
}

func serveIndex(w http.ResponseWriter, r *http.Request, cfg config.Config, data []byte) {
	data = bytes.ReplaceAll(data, []byte("%OLCPANEL_BASE_PATH%"), []byte(cfg.BasePath))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(data))
}

func parseLogQuery(r *http.Request) (observability.LogQuery, error) {
	values := r.URL.Query()
	query := observability.LogQuery{
		Level:      strings.TrimSpace(values.Get("level")),
		Source:     strings.TrimSpace(values.Get("source")),
		ClientID:   strings.TrimSpace(values.Get("client_id")),
		LocationID: strings.TrimSpace(values.Get("location_id")),
		Query:      strings.TrimSpace(values.Get("q")),
	}
	if raw := strings.TrimSpace(values.Get("limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 0 {
			return observability.LogQuery{}, errors.New("limit must be a non-negative integer")
		}
		query.Limit = limit
	}
	if raw := strings.TrimSpace(values.Get("since")); raw != "" {
		parsed, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return observability.LogQuery{}, errors.New("since must be RFC3339 time")
		}
		query.Since = parsed
	}
	if raw := strings.TrimSpace(values.Get("until")); raw != "" {
		parsed, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return observability.LogQuery{}, errors.New("until must be RFC3339 time")
		}
		query.Until = parsed
	}
	return query, nil
}

type credentialsRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type sessionResponse struct {
	Username  string `json:"username"`
	CSRFToken string `json:"csrf_token"`
}

type apiKeyCreateRequest struct {
	Name string `json:"name"`
}

type apiKeyCreateResponse struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Token string `json:"token"`
}

type locationRequest struct {
	Name             string          `json:"name"`
	Enabled          *bool           `json:"enabled"`
	Provider         string          `json:"provider"`
	Transport        string          `json:"transport"`
	RoomID           string          `json:"room_id"`
	CryptoKey        string          `json:"crypto_key"`
	TransportPayload json.RawMessage `json:"transport_payload"`
	DNS              string          `json:"dns"`
	SpeedLimitBPS    *int64          `json:"speed_limit_bps"`
}

type rotateRequest struct {
	RotateRooms             bool `json:"rotate_rooms"`
	RotateSubscriptionToken bool `json:"rotate_subscription_token"`
}

type restoreRequest struct {
	BackupID int64 `json:"backup_id"`
}

type requestAuth struct {
	session auth.Session
	apiKey  *auth.APIKey
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, value any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			http.Error(w, "JSON body is too large", http.StatusRequestEntityTooLarge)
			return false
		}
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return false
	}
	var extra struct{}
	if err := decoder.Decode(&extra); err != io.EOF {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			http.Error(w, "JSON body is too large", http.StatusRequestEntityTooLarge)
			return false
		}
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return false
	}
	return true
}

func decodeLocationInput(w http.ResponseWriter, r *http.Request) (clients.LocationInput, bool) {
	var payload locationRequest
	if !decodeJSON(w, r, &payload) {
		return clients.LocationInput{}, false
	}
	return clients.LocationInput{
		Name:             payload.Name,
		Enabled:          payload.Enabled,
		Provider:         payload.Provider,
		Transport:        payload.Transport,
		RoomID:           payload.RoomID,
		CryptoKey:        payload.CryptoKey,
		TransportPayload: strings.TrimSpace(string(payload.TransportPayload)),
		DNS:              payload.DNS,
		SpeedLimitBPS:    payload.SpeedLimitBPS,
	}, true
}

func (deps dependencies) requireAdmin(w http.ResponseWriter, r *http.Request, requireCSRF bool) (requestAuth, bool) {
	if deps.db == nil {
		http.Error(w, "database is unavailable", http.StatusServiceUnavailable)
		return requestAuth{}, false
	}
	usersExist, err := auth.UsersExist(r.Context(), deps.db)
	if err != nil {
		http.Error(w, "failed to read setup state", http.StatusInternalServerError)
		return requestAuth{}, false
	}
	if !usersExist {
		http.Error(w, "setup required", http.StatusForbidden)
		return requestAuth{}, false
	}
	ctx, ok := deps.authenticateOptional(r)
	if !ok {
		if bearerToken(r) != "" && deps.apiKeyFailLimiter != nil && !deps.apiKeyFailLimiter.Allow(clientKey(r, "api-key")) {
			http.Error(w, "too many api key failures", http.StatusTooManyRequests)
			return requestAuth{}, false
		}
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return requestAuth{}, false
	}
	if ctx.apiKey != nil {
		return ctx, true
	}
	if requireCSRF && !deps.verifyBrowserSafety(w, r, ctx.session) {
		return requestAuth{}, false
	}
	return ctx, true
}

func (deps dependencies) requireSessionAdmin(w http.ResponseWriter, r *http.Request, requireCSRF bool) (requestAuth, bool) {
	ctx, ok := deps.requireAdmin(w, r, requireCSRF)
	if !ok {
		return requestAuth{}, false
	}
	if ctx.apiKey != nil {
		http.Error(w, "session authentication required", http.StatusForbidden)
		return requestAuth{}, false
	}
	return ctx, true
}

func (deps dependencies) overlayLocations(locations []clients.Location) []clients.Location {
	statuses := deps.statusSnapshot()
	for i := range locations {
		locations[i] = overlayLocationStatus(locations[i], statuses)
	}
	return locations
}

func (deps dependencies) overlayLocation(location clients.Location) clients.Location {
	return overlayLocationStatus(location, deps.statusSnapshot())
}

func (deps dependencies) statusSnapshot() map[string]supervisor.ProcessStatus {
	if deps.statuses == nil {
		return nil
	}
	return deps.statuses.StatusSnapshot()
}

func overlayLocationStatus(location clients.Location, statuses map[string]supervisor.ProcessStatus) clients.Location {
	switch statuses[location.ID] {
	case supervisor.ProcessRunning:
		location.RuntimeStatus = string(supervisor.ProcessRunning)
	case supervisor.ProcessStopped:
		location.RuntimeStatus = string(supervisor.ProcessStopped)
	case supervisor.ProcessFailed:
		location.RuntimeStatus = string(supervisor.ProcessFailed)
	default:
		if location.RuntimeStatus == "" {
			location.RuntimeStatus = string(supervisor.ProcessPending)
		}
	}
	return location
}

func (deps dependencies) authenticateOptional(r *http.Request) (requestAuth, bool) {
	if deps.db == nil {
		return requestAuth{}, false
	}
	if token := bearerToken(r); token != "" {
		key, err := auth.AuthenticateAPIKey(r.Context(), deps.db, token)
		if err != nil {
			return requestAuth{}, false
		}
		return requestAuth{apiKey: &key}, true
	}
	cookie, err := r.Cookie(auth.SessionCookieName)
	if err != nil || cookie.Value == "" {
		return requestAuth{}, false
	}
	session, err := auth.AuthenticateSession(r.Context(), deps.db, cookie.Value)
	if err != nil {
		return requestAuth{}, false
	}
	return requestAuth{session: session}, true
}

func (deps dependencies) verifyBrowserSafety(w http.ResponseWriter, r *http.Request, session auth.Session) bool {
	if !sameOrigin(r) {
		http.Error(w, "cross-origin request rejected", http.StatusForbidden)
		return false
	}
	if !auth.VerifyCSRF(session, r.Header.Get("X-CSRF-Token")) {
		http.Error(w, "csrf token required", http.StatusForbidden)
		return false
	}
	return true
}

func bearerToken(r *http.Request) string {
	value := r.Header.Get("Authorization")
	if value == "" {
		return ""
	}
	prefix, token, ok := strings.Cut(value, " ")
	if !ok || !strings.EqualFold(prefix, "Bearer") {
		return ""
	}
	return strings.TrimSpace(token)
}

func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return origin == scheme+"://"+r.Host
}

func (deps dependencies) cookiePath() string {
	if deps.basePath == "" {
		return "/"
	}
	return deps.basePath
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, path string, value string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookieName,
		Value:    value,
		Path:     path,
		Expires:  expires,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   cookieSecure(r),
	})
}

func clearSessionCookie(w http.ResponseWriter, r *http.Request, path string) {
	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookieName,
		Value:    "",
		Path:     path,
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   cookieSecure(r),
	})
}

func cookieSecure(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func clientKey(r *http.Request, scope string) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return scope + ":" + r.RemoteAddr
	}
	return scope + ":" + host
}

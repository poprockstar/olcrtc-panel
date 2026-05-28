package server

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"olcpanel/internal/auth"
	"olcpanel/internal/clients"
	"olcpanel/internal/config"
	"olcpanel/internal/storage"
)

type StateResponse struct {
	Service       string `json:"service"`
	APIVersion    string `json:"api_version"`
	SetupRequired bool   `json:"setup_required"`
	BindAddress   string `json:"bind_address"`
	Authenticated bool   `json:"authenticated,omitempty"`
}

type Option func(*dependencies)

type dependencies struct {
	db                *sql.DB
	setupLimiter      *auth.RateLimiter
	loginLimiter      *auth.RateLimiter
	apiKeyFailLimiter *auth.RateLimiter
}

func WithDatabase(db *sql.DB) Option {
	return func(deps *dependencies) {
		deps.db = db
	}
}

func New(cfg config.Config, assets fs.FS, options ...Option) http.Handler {
	deps := dependencies{
		setupLimiter:      auth.NewRateLimiter(5, time.Minute),
		loginLimiter:      auth.NewRateLimiter(5, time.Minute),
		apiKeyFailLimiter: auth.NewRateLimiter(10, time.Minute),
	}
	for _, option := range options {
		option(&deps)
	}

	mux := http.NewServeMux()

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
			Authenticated: authenticated,
		})
	})

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
		setSessionCookie(w, r, session.ID, session.ExpiresAt)
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
		setSessionCookie(w, r, session.ID, session.ExpiresAt)
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
		clearSessionCookie(w, r)
		w.WriteHeader(http.StatusNoContent)
	})

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
		locations, err := clients.RotateClientLocations(r.Context(), deps.db, r.PathValue("id"), input.RotateRooms)
		if errors.Is(err, clients.ErrNotFound) {
			http.Error(w, "client not found", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, "failed to rotate locations", http.StatusInternalServerError)
			return
		}
		writeJSON(w, locations)
	})

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
		if err := auth.RevokeAPIKey(r.Context(), deps.db, id); err != nil {
			http.Error(w, "failed to revoke api key", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
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
}

type rotateRequest struct {
	RotateRooms bool `json:"rotate_rooms"`
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
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
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

func setSessionCookie(w http.ResponseWriter, r *http.Request, value string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookieName,
		Value:    value,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   cookieSecure(r),
	})
}

func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookieName,
		Value:    "",
		Path:     "/",
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
	host := r.RemoteAddr
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); forwarded != "" {
		host, _, _ = strings.Cut(forwarded, ",")
		host = strings.TrimSpace(host)
	}
	return scope + ":" + host
}

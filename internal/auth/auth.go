package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	SessionCookieName = "olcpanel_session"
	apiTokenPrefix    = "olcp_"
)

var (
	ErrSetupComplete     = errors.New("setup is already complete")
	ErrInvalidCredential = errors.New("invalid username or password")
	ErrUnauthorized      = errors.New("unauthorized")
	ErrNotFound          = errors.New("not found")
)

type User struct {
	ID       int64
	Username string
	Role     string
}

type Session struct {
	ID            string
	UserID        int64
	CSRFToken     string
	CSRFTokenHash string
	ExpiresAt     time.Time
}

type APIKey struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

type RateLimiter struct {
	mu       sync.Mutex
	limit    int
	window   time.Duration
	attempts map[string][]time.Time
}

func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{limit: limit, window: window, attempts: make(map[string][]time.Time)}
}

func (limiter *RateLimiter) Allow(key string) bool {
	if limiter == nil {
		return true
	}
	limiter.mu.Lock()
	defer limiter.mu.Unlock()

	now := time.Now().UTC()
	cutoff := now.Add(-limiter.window)
	for attemptKey, attempts := range limiter.attempts {
		recent := attempts[:0]
		for _, attempt := range attempts {
			if attempt.After(cutoff) {
				recent = append(recent, attempt)
			}
		}
		if len(recent) == 0 {
			delete(limiter.attempts, attemptKey)
			continue
		}
		limiter.attempts[attemptKey] = recent
	}
	recent := limiter.attempts[key][:0]
	for _, attempt := range limiter.attempts[key] {
		if attempt.After(cutoff) {
			recent = append(recent, attempt)
		}
	}
	if len(recent) >= limiter.limit {
		limiter.attempts[key] = recent
		return false
	}
	limiter.attempts[key] = append(recent, now)
	return true
}

func UsersExist(ctx context.Context, db *sql.DB) (bool, error) {
	count, err := UserCount(ctx, db)
	return count > 0, err
}

func UserCount(ctx context.Context, db *sql.DB) (int, error) {
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count users: %w", err)
	}
	return count, nil
}

func CreateFirstAdmin(ctx context.Context, db *sql.DB, username, password string) (User, error) {
	username = strings.TrimSpace(username)
	if err := validateCredentials(username, password); err != nil {
		return User{}, err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, fmt.Errorf("begin setup transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return User{}, fmt.Errorf("count users: %w", err)
	}
	if count > 0 {
		return User{}, ErrSetupComplete
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return User{}, fmt.Errorf("hash password: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
INSERT INTO users(username, password_hash, role, updated_at)
VALUES (?, ?, 'admin', CURRENT_TIMESTAMP)`, username, string(hash))
	if err != nil {
		return User{}, fmt.Errorf("insert first admin: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return User{}, fmt.Errorf("read first admin id: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return User{}, fmt.Errorf("commit setup transaction: %w", err)
	}
	return User{ID: id, Username: username, Role: "admin"}, nil
}

func ResetAdmin(ctx context.Context, db *sql.DB, username, password string) (User, error) {
	username = strings.TrimSpace(username)
	if err := validateCredentials(username, password); err != nil {
		return User{}, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return User{}, fmt.Errorf("hash password: %w", err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, fmt.Errorf("begin reset admin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `
UPDATE users
SET password_hash = ?, role = 'admin', updated_at = CURRENT_TIMESTAMP
WHERE username = ?`, string(hash), username)
	if err != nil {
		return User{}, fmt.Errorf("update admin: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return User{}, fmt.Errorf("read updated admin count: %w", err)
	}
	if affected == 0 {
		result, err = tx.ExecContext(ctx, `
INSERT INTO users(username, password_hash, role, updated_at)
VALUES (?, ?, 'admin', CURRENT_TIMESTAMP)`, username, string(hash))
		if err != nil {
			return User{}, fmt.Errorf("insert admin: %w", err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			return User{}, fmt.Errorf("read admin id: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return User{}, fmt.Errorf("commit reset admin transaction: %w", err)
		}
		return User{ID: id, Username: username, Role: "admin"}, nil
	}
	var user User
	if err := tx.QueryRowContext(ctx, `SELECT id, username, role FROM users WHERE username = ?`, username).Scan(&user.ID, &user.Username, &user.Role); err != nil {
		return User{}, fmt.Errorf("read admin: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return User{}, fmt.Errorf("commit reset admin transaction: %w", err)
	}
	return user, nil
}

func VerifyLogin(ctx context.Context, db *sql.DB, username, password string) (User, error) {
	username = strings.TrimSpace(username)
	var user User
	var hash string
	err := db.QueryRowContext(ctx, `SELECT id, username, role, password_hash FROM users WHERE username = ?`, username).Scan(&user.ID, &user.Username, &user.Role, &hash)
	if err == sql.ErrNoRows {
		return User{}, ErrInvalidCredential
	}
	if err != nil {
		return User{}, fmt.Errorf("read user: %w", err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		return User{}, ErrInvalidCredential
	}
	return user, nil
}

func CreateSession(ctx context.Context, db *sql.DB, userID int64, ttl time.Duration) (Session, error) {
	id, err := randomToken("")
	if err != nil {
		return Session{}, err
	}
	csrfToken, err := randomToken("")
	if err != nil {
		return Session{}, err
	}
	session := Session{
		ID:            id,
		UserID:        userID,
		CSRFToken:     csrfToken,
		CSRFTokenHash: HashToken(csrfToken),
		ExpiresAt:     time.Now().UTC().Add(ttl),
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO sessions(id, user_id, csrf_token_hash, expires_at)
VALUES (?, ?, ?, ?)`, session.ID, session.UserID, session.CSRFTokenHash, formatTime(session.ExpiresAt)); err != nil {
		return Session{}, fmt.Errorf("insert session: %w", err)
	}
	return session, nil
}

func AuthenticateSession(ctx context.Context, db *sql.DB, sessionID string) (Session, error) {
	var session Session
	var expires string
	err := db.QueryRowContext(ctx, `
SELECT id, user_id, csrf_token_hash, expires_at
FROM sessions
WHERE id = ?`, sessionID).Scan(&session.ID, &session.UserID, &session.CSRFTokenHash, &expires)
	if err == sql.ErrNoRows {
		return Session{}, ErrUnauthorized
	}
	if err != nil {
		return Session{}, fmt.Errorf("read session: %w", err)
	}
	parsed, err := parseTime(expires)
	if err != nil {
		return Session{}, fmt.Errorf("parse session expiry: %w", err)
	}
	session.ExpiresAt = parsed
	if !parsed.After(time.Now().UTC()) {
		_ = RevokeSession(ctx, db, session.ID)
		return Session{}, ErrUnauthorized
	}
	return session, nil
}

func RevokeSession(ctx context.Context, db *sql.DB, sessionID string) error {
	if _, err := db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, sessionID); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

func VerifyCSRF(session Session, csrfToken string) bool {
	if session.CSRFTokenHash == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(HashToken(csrfToken)), []byte(session.CSRFTokenHash)) == 1
}

func CreateAPIKey(ctx context.Context, db *sql.DB, name string) (APIKey, string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 80 {
		return APIKey{}, "", errors.New("api key name is required and must be at most 80 characters")
	}
	rawToken, err := randomToken(apiTokenPrefix)
	if err != nil {
		return APIKey{}, "", err
	}
	result, err := db.ExecContext(ctx, `
INSERT INTO api_keys(name, token_hash)
VALUES (?, ?)`, name, HashToken(rawToken))
	if err != nil {
		return APIKey{}, "", fmt.Errorf("insert api key: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return APIKey{}, "", fmt.Errorf("read api key id: %w", err)
	}
	key := APIKey{ID: id, Name: name, CreatedAt: time.Now().UTC()}
	return key, rawToken, nil
}

func ListAPIKeys(ctx context.Context, db *sql.DB) ([]APIKey, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, name, created_at, last_used_at, revoked_at FROM api_keys ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list api keys: %w", err)
	}
	defer rows.Close()

	var keys []APIKey
	for rows.Next() {
		var key APIKey
		var created string
		var lastUsed, revoked sql.NullString
		if err := rows.Scan(&key.ID, &key.Name, &created, &lastUsed, &revoked); err != nil {
			return nil, fmt.Errorf("scan api key: %w", err)
		}
		key.CreatedAt, _ = parseTime(created)
		key.LastUsedAt = nullableTime(lastUsed)
		key.RevokedAt = nullableTime(revoked)
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate api keys: %w", err)
	}
	return keys, nil
}

func AuthenticateAPIKey(ctx context.Context, db *sql.DB, rawToken string) (APIKey, error) {
	if !strings.HasPrefix(rawToken, apiTokenPrefix) {
		return APIKey{}, ErrUnauthorized
	}
	var key APIKey
	var created string
	err := db.QueryRowContext(ctx, `
SELECT id, name, created_at
FROM api_keys
WHERE token_hash = ? AND revoked_at IS NULL`, HashToken(rawToken)).Scan(&key.ID, &key.Name, &created)
	if err == sql.ErrNoRows {
		return APIKey{}, ErrUnauthorized
	}
	if err != nil {
		return APIKey{}, fmt.Errorf("read api key: %w", err)
	}
	key.CreatedAt, _ = parseTime(created)
	if _, err := db.ExecContext(ctx, `UPDATE api_keys SET last_used_at = CURRENT_TIMESTAMP WHERE id = ?`, key.ID); err != nil {
		return APIKey{}, fmt.Errorf("touch api key: %w", err)
	}
	return key, nil
}

func RevokeAPIKey(ctx context.Context, db *sql.DB, id int64) error {
	result, err := db.ExecContext(ctx, `UPDATE api_keys SET revoked_at = CURRENT_TIMESTAMP WHERE id = ? AND revoked_at IS NULL`, id)
	if err != nil {
		return fmt.Errorf("revoke api key: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read revoked api key count: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func validateCredentials(username, password string) error {
	if len(username) < 3 || len(username) > 64 {
		return errors.New("username must be 3-64 characters")
	}
	if len(password) < 12 {
		return errors.New("password must be at least 12 characters")
	}
	return nil
}

func randomToken(prefix string) (string, error) {
	var data [32]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", fmt.Errorf("generate secure token: %w", err)
	}
	return prefix + base64.RawURLEncoding.EncodeToString(data[:]), nil
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

func nullableTime(value sql.NullString) *time.Time {
	if !value.Valid {
		return nil
	}
	parsed, err := parseTime(value.String)
	if err != nil {
		return nil
	}
	return &parsed
}

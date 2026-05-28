package auth_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"olcpanel/internal/auth"
	"olcpanel/internal/storage"
)

func TestCreateFirstAdminOnlyOnceAndVerifyLogin(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)

	user, err := auth.CreateFirstAdmin(ctx, db, "admin", "correct horse battery")
	if err != nil {
		t.Fatalf("CreateFirstAdmin returned error: %v", err)
	}
	if user.ID == 0 || user.Username != "admin" || user.Role != "admin" {
		t.Fatalf("user = %#v, want first admin", user)
	}

	if _, err := auth.CreateFirstAdmin(ctx, db, "other", "correct horse battery"); err == nil {
		t.Fatal("duplicate CreateFirstAdmin returned nil error")
	}

	if _, err := auth.VerifyLogin(ctx, db, "admin", "wrong horse battery"); err == nil {
		t.Fatal("VerifyLogin accepted wrong password")
	}
	verified, err := auth.VerifyLogin(ctx, db, "admin", "correct horse battery")
	if err != nil {
		t.Fatalf("VerifyLogin returned error: %v", err)
	}
	if verified.ID != user.ID {
		t.Fatalf("verified ID = %d, want %d", verified.ID, user.ID)
	}
}

func TestSessionsExpire(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)
	user, err := auth.CreateFirstAdmin(ctx, db, "admin", "correct horse battery")
	if err != nil {
		t.Fatalf("CreateFirstAdmin returned error: %v", err)
	}

	session, err := auth.CreateSession(ctx, db, user.ID, -time.Minute)
	if err != nil {
		t.Fatalf("CreateSession returned error: %v", err)
	}
	if _, err := auth.AuthenticateSession(ctx, db, session.ID); err == nil {
		t.Fatal("AuthenticateSession accepted expired session")
	}
}

func TestVerifyCSRFAcceptsOnlyMatchingToken(t *testing.T) {
	session := auth.Session{CSRFTokenHash: auth.HashToken("valid-token")}

	if !auth.VerifyCSRF(session, "valid-token") {
		t.Fatal("VerifyCSRF rejected a valid token")
	}
	if auth.VerifyCSRF(session, "invalid-token") {
		t.Fatal("VerifyCSRF accepted an invalid token")
	}
}

func TestRevokedAPIKeyStopsAuthenticating(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)

	key, rawToken, err := auth.CreateAPIKey(ctx, db, "deploy")
	if err != nil {
		t.Fatalf("CreateAPIKey returned error: %v", err)
	}
	if rawToken == "" || key.ID == 0 {
		t.Fatalf("key = %#v token = %q, want persisted key and raw token", key, rawToken)
	}
	if _, err := auth.AuthenticateAPIKey(ctx, db, rawToken); err != nil {
		t.Fatalf("AuthenticateAPIKey returned error before revoke: %v", err)
	}
	if err := auth.RevokeAPIKey(ctx, db, key.ID); err != nil {
		t.Fatalf("RevokeAPIKey returned error: %v", err)
	}
	if _, err := auth.AuthenticateAPIKey(ctx, db, rawToken); err == nil {
		t.Fatal("AuthenticateAPIKey accepted revoked key")
	}
}

func TestRevokeAPIKeyReturnsNotFoundForMissingOrRevokedKey(t *testing.T) {
	ctx := context.Background()
	db := testDB(t)

	if err := auth.RevokeAPIKey(ctx, db, 999); !errors.Is(err, auth.ErrNotFound) {
		t.Fatalf("missing key error = %v, want ErrNotFound", err)
	}

	key, _, err := auth.CreateAPIKey(ctx, db, "deploy")
	if err != nil {
		t.Fatalf("CreateAPIKey returned error: %v", err)
	}
	if err := auth.RevokeAPIKey(ctx, db, key.ID); err != nil {
		t.Fatalf("RevokeAPIKey returned error: %v", err)
	}
	if err := auth.RevokeAPIKey(ctx, db, key.ID); !errors.Is(err, auth.ErrNotFound) {
		t.Fatalf("revoked key error = %v, want ErrNotFound", err)
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

package main

import (
	"context"
	"strings"
	"testing"

	"olcpanel/internal/storage"
)

func TestServeRejectsUnexpectedArgument(t *testing.T) {
	err := run([]string{"serve", "unexpected"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "unexpected argument") {
		t.Fatalf("expected unexpected argument error, got %q", err.Error())
	}
}

func TestMigrateCommandRunsMigrations(t *testing.T) {
	databaseURL := "sqlite:///" + strings.ReplaceAll(t.TempDir(), "\\", "/") + "/panel.db"

	if err := run([]string{"migrate", "--database-url", databaseURL}); err != nil {
		t.Fatalf("run migrate returned error: %v", err)
	}

	db, err := storage.Open(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	settings, err := storage.GetSettings(context.Background(), db)
	if err != nil {
		t.Fatalf("GetSettings returned error: %v", err)
	}
	if settings.UILocale != "en" {
		t.Fatalf("UILocale = %q, want migrated default", settings.UILocale)
	}
}

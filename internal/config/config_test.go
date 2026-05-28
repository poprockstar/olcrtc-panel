package config_test

import (
	"testing"

	"olcpanel/internal/config"
)

func TestLoadUsesLocalBindDefault(t *testing.T) {
	t.Setenv("OLCPANEL_BIND", "")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.BindAddress != "127.0.0.1:8888" {
		t.Fatalf("BindAddress = %q, want %q", cfg.BindAddress, "127.0.0.1:8888")
	}
}

func TestLoadAllowsBindOverride(t *testing.T) {
	t.Setenv("OLCPANEL_BIND", "127.0.0.1:9999")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.BindAddress != "127.0.0.1:9999" {
		t.Fatalf("BindAddress = %q, want %q", cfg.BindAddress, "127.0.0.1:9999")
	}
}

func TestLoadUsesDefaultDatabaseURL(t *testing.T) {
	t.Setenv("OLCPANEL_DATABASE_URL", "")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.DatabaseURL != "sqlite:///etc/olcpanel/panel.db" {
		t.Fatalf("DatabaseURL = %q, want default sqlite URL", cfg.DatabaseURL)
	}
}

func TestLoadAllowsDatabaseURLEnvOverride(t *testing.T) {
	t.Setenv("OLCPANEL_DATABASE_URL", "sqlite:///tmp/olcpanel-test.db")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.DatabaseURL != "sqlite:///tmp/olcpanel-test.db" {
		t.Fatalf("DatabaseURL = %q, want env override", cfg.DatabaseURL)
	}
}

func TestLoadAllowsDatabaseURLCLIOverride(t *testing.T) {
	t.Setenv("OLCPANEL_DATABASE_URL", "sqlite:///tmp/from-env.db")

	cfg, err := config.LoadWithOptions(config.LoadOptions{
		DatabaseURL: "sqlite:///tmp/from-cli.db",
	})
	if err != nil {
		t.Fatalf("LoadWithOptions returned error: %v", err)
	}

	if cfg.DatabaseURL != "sqlite:///tmp/from-cli.db" {
		t.Fatalf("DatabaseURL = %q, want CLI override", cfg.DatabaseURL)
	}
}

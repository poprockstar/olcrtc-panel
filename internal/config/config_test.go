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

func TestLoadUsesDefaultRuntimeConfig(t *testing.T) {
	t.Setenv("OLCPANEL_RUNTIME_DIR", "")
	t.Setenv("OLCPANEL_OLCRTC_BINARY", "")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.RuntimeDir != "/var/lib/olcpanel/runtime" {
		t.Fatalf("RuntimeDir = %q, want default runtime dir", cfg.RuntimeDir)
	}
	if cfg.OlcRTCBinary != "olcrtc" {
		t.Fatalf("OlcRTCBinary = %q, want default binary", cfg.OlcRTCBinary)
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

func TestLoadAllowsRuntimeEnvOverrides(t *testing.T) {
	t.Setenv("OLCPANEL_RUNTIME_DIR", "/tmp/olcpanel-runtime")
	t.Setenv("OLCPANEL_OLCRTC_BINARY", "/usr/local/bin/olcrtc")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.RuntimeDir != "/tmp/olcpanel-runtime" {
		t.Fatalf("RuntimeDir = %q, want env override", cfg.RuntimeDir)
	}
	if cfg.OlcRTCBinary != "/usr/local/bin/olcrtc" {
		t.Fatalf("OlcRTCBinary = %q, want env override", cfg.OlcRTCBinary)
	}
}

func TestLoadAllowsRuntimeCLIOverrides(t *testing.T) {
	t.Setenv("OLCPANEL_RUNTIME_DIR", "/tmp/from-env")
	t.Setenv("OLCPANEL_OLCRTC_BINARY", "olcrtc-from-env")

	cfg, err := config.LoadWithOptions(config.LoadOptions{
		RuntimeDir:   "/tmp/from-cli",
		OlcRTCBinary: "olcrtc-from-cli",
	})
	if err != nil {
		t.Fatalf("LoadWithOptions returned error: %v", err)
	}

	if cfg.RuntimeDir != "/tmp/from-cli" {
		t.Fatalf("RuntimeDir = %q, want CLI override", cfg.RuntimeDir)
	}
	if cfg.OlcRTCBinary != "olcrtc-from-cli" {
		t.Fatalf("OlcRTCBinary = %q, want CLI override", cfg.OlcRTCBinary)
	}
}

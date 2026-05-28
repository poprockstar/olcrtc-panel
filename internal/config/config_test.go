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

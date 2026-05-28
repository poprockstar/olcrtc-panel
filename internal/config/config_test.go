package config_test

import (
	"strings"
	"testing"
	"time"

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

func TestLoadUsesDefaultNetworkCIDR(t *testing.T) {
	t.Setenv("OLCPANEL_NETWORK_CIDR", "")
	t.Setenv("OLCPANEL_TRAFFIC_SAMPLE_INTERVAL", "")
	t.Setenv("OLCPANEL_LOG_PATH", "")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.NetworkCIDR != "10.255.0.0/16" {
		t.Fatalf("NetworkCIDR = %q, want default runtime network", cfg.NetworkCIDR)
	}
	if cfg.TrafficSampleInterval != 30*time.Second {
		t.Fatalf("TrafficSampleInterval = %s, want 30s", cfg.TrafficSampleInterval)
	}
	if cfg.LogPath != "/var/log/olcpanel/panel.log" {
		t.Fatalf("LogPath = %q, want default panel log path", cfg.LogPath)
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

func TestLoadAllowsNetworkCIDROverrides(t *testing.T) {
	t.Setenv("OLCPANEL_NETWORK_CIDR", "10.88.0.0/16")

	cfg, err := config.LoadWithOptions(config.LoadOptions{
		NetworkCIDR: "10.99.0.0/16",
	})
	if err != nil {
		t.Fatalf("LoadWithOptions returned error: %v", err)
	}

	if cfg.NetworkCIDR != "10.99.0.0/16" {
		t.Fatalf("NetworkCIDR = %q, want CLI override", cfg.NetworkCIDR)
	}
}

func TestLoadAllowsTrafficSampleIntervalEnvOverride(t *testing.T) {
	t.Setenv("OLCPANEL_TRAFFIC_SAMPLE_INTERVAL", "45s")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.TrafficSampleInterval != 45*time.Second {
		t.Fatalf("TrafficSampleInterval = %s, want 45s", cfg.TrafficSampleInterval)
	}
}

func TestLoadAllowsTrafficSampleIntervalCLIOverride(t *testing.T) {
	t.Setenv("OLCPANEL_TRAFFIC_SAMPLE_INTERVAL", "45s")

	cfg, err := config.LoadWithOptions(config.LoadOptions{
		TrafficSampleInterval: "2m",
	})
	if err != nil {
		t.Fatalf("LoadWithOptions returned error: %v", err)
	}

	if cfg.TrafficSampleInterval != 2*time.Minute {
		t.Fatalf("TrafficSampleInterval = %s, want 2m", cfg.TrafficSampleInterval)
	}
}

func TestLoadAllowsLogPathEnvOverride(t *testing.T) {
	t.Setenv("OLCPANEL_LOG_PATH", "/tmp/olcpanel/panel.log")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.LogPath != "/tmp/olcpanel/panel.log" {
		t.Fatalf("LogPath = %q, want env override", cfg.LogPath)
	}
}

func TestLoadAllowsLogPathCLIOverride(t *testing.T) {
	t.Setenv("OLCPANEL_LOG_PATH", "/tmp/from-env.log")

	cfg, err := config.LoadWithOptions(config.LoadOptions{
		LogPath: "/tmp/from-cli.log",
	})
	if err != nil {
		t.Fatalf("LoadWithOptions returned error: %v", err)
	}

	if cfg.LogPath != "/tmp/from-cli.log" {
		t.Fatalf("LogPath = %q, want CLI override", cfg.LogPath)
	}
}

func TestLoadRejectsInvalidTrafficSampleInterval(t *testing.T) {
	for _, value := range []string{"soon", "0s", "-1s"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("OLCPANEL_TRAFFIC_SAMPLE_INTERVAL", value)

			_, err := config.Load()
			if err == nil {
				t.Fatal("Load returned nil error, want invalid interval error")
			}
			if !strings.Contains(err.Error(), "traffic sample interval") {
				t.Fatalf("error = %q, want traffic sample interval context", err.Error())
			}
		})
	}
}

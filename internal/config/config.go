package config

import "os"

const (
	DefaultBindAddress  = "127.0.0.1:8888"
	DefaultDatabaseURL  = "sqlite:///etc/olcpanel/panel.db"
	DefaultRuntimeDir   = "/var/lib/olcpanel/runtime"
	DefaultOlcRTCBinary = "olcrtc"
	DefaultNetworkCIDR  = "10.255.0.0/16"
)

type Config struct {
	BindAddress  string
	DatabaseURL  string
	RuntimeDir   string
	OlcRTCBinary string
	NetworkCIDR  string
}

type LoadOptions struct {
	BindAddress  string
	DatabaseURL  string
	RuntimeDir   string
	OlcRTCBinary string
	NetworkCIDR  string
}

func Load() (Config, error) {
	return LoadWithOptions(LoadOptions{})
}

func LoadWithOptions(options LoadOptions) (Config, error) {
	bindAddress := os.Getenv("OLCPANEL_BIND")
	if bindAddress == "" {
		bindAddress = DefaultBindAddress
	}
	if options.BindAddress != "" {
		bindAddress = options.BindAddress
	}

	databaseURL := os.Getenv("OLCPANEL_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = DefaultDatabaseURL
	}
	if options.DatabaseURL != "" {
		databaseURL = options.DatabaseURL
	}

	runtimeDir := os.Getenv("OLCPANEL_RUNTIME_DIR")
	if runtimeDir == "" {
		runtimeDir = DefaultRuntimeDir
	}
	if options.RuntimeDir != "" {
		runtimeDir = options.RuntimeDir
	}

	olcrtcBinary := os.Getenv("OLCPANEL_OLCRTC_BINARY")
	if olcrtcBinary == "" {
		olcrtcBinary = DefaultOlcRTCBinary
	}
	if options.OlcRTCBinary != "" {
		olcrtcBinary = options.OlcRTCBinary
	}

	networkCIDR := os.Getenv("OLCPANEL_NETWORK_CIDR")
	if networkCIDR == "" {
		networkCIDR = DefaultNetworkCIDR
	}
	if options.NetworkCIDR != "" {
		networkCIDR = options.NetworkCIDR
	}

	return Config{
		BindAddress:  bindAddress,
		DatabaseURL:  databaseURL,
		RuntimeDir:   runtimeDir,
		OlcRTCBinary: olcrtcBinary,
		NetworkCIDR:  networkCIDR,
	}, nil
}

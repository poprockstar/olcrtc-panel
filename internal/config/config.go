package config

import (
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	DefaultBindAddress           = "127.0.0.1:8888"
	DefaultDatabaseURL           = "sqlite:///etc/olcpanel/panel.db"
	DefaultRuntimeDir            = "/var/lib/olcpanel/runtime"
	DefaultOlcRTCBinary          = "olcrtc"
	DefaultNetworkCIDR           = "10.255.0.0/16"
	DefaultTrafficSampleInterval = 30 * time.Second
	DefaultLogPath               = "/var/log/olcpanel/panel.log"
)

type Config struct {
	BindAddress           string
	BasePath              string
	DatabaseURL           string
	RuntimeDir            string
	OlcRTCBinary          string
	NetworkCIDR           string
	TrafficSampleInterval time.Duration
	LogPath               string
}

type LoadOptions struct {
	BindAddress           string
	BasePath              string
	DatabaseURL           string
	RuntimeDir            string
	OlcRTCBinary          string
	NetworkCIDR           string
	TrafficSampleInterval string
	LogPath               string
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

	basePath, err := NormalizeBasePath(os.Getenv("OLCPANEL_BASE_PATH"))
	if err != nil {
		return Config{}, err
	}
	if options.BasePath != "" {
		basePath, err = NormalizeBasePath(options.BasePath)
		if err != nil {
			return Config{}, err
		}
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

	trafficSampleInterval, err := parseTrafficSampleInterval(os.Getenv("OLCPANEL_TRAFFIC_SAMPLE_INTERVAL"))
	if err != nil {
		return Config{}, err
	}
	if options.TrafficSampleInterval != "" {
		trafficSampleInterval, err = parseTrafficSampleInterval(options.TrafficSampleInterval)
		if err != nil {
			return Config{}, err
		}
	}

	logPath := os.Getenv("OLCPANEL_LOG_PATH")
	if logPath == "" {
		logPath = DefaultLogPath
	}
	if options.LogPath != "" {
		logPath = options.LogPath
	}

	return Config{
		BindAddress:           bindAddress,
		BasePath:              basePath,
		DatabaseURL:           databaseURL,
		RuntimeDir:            runtimeDir,
		OlcRTCBinary:          olcrtcBinary,
		NetworkCIDR:           networkCIDR,
		TrafficSampleInterval: trafficSampleInterval,
		LogPath:               logPath,
	}, nil
}

func NormalizeBasePath(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" || value == "/" {
		return "", nil
	}
	if strings.ContainsAny(value, " \t\r\n?#") {
		return "", fmt.Errorf("base path must be a clean URI path without spaces, query, or fragment")
	}
	if !strings.HasPrefix(value, "/") {
		value = "/" + value
	}
	value = strings.TrimRight(value, "/")
	if value == "" {
		return "", nil
	}
	segments := strings.Split(strings.TrimPrefix(value, "/"), "/")
	if len(segments) == 0 || segments[0] == "" {
		return "", fmt.Errorf("base path must contain a path segment")
	}
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return "", fmt.Errorf("base path must not contain empty, current, or parent segments")
		}
	}
	switch segments[0] {
	case "api", "sub", "c", "assets":
		return "", fmt.Errorf("base path %q uses a reserved routing prefix", value)
	}
	return value, nil
}

func parseTrafficSampleInterval(raw string) (time.Duration, error) {
	if raw == "" {
		return DefaultTrafficSampleInterval, nil
	}
	interval, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("traffic sample interval must be a positive duration: %w", err)
	}
	if interval <= 0 {
		return 0, fmt.Errorf("traffic sample interval must be positive")
	}
	return interval, nil
}

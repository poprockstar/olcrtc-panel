package config

import "os"

const (
	DefaultBindAddress = "127.0.0.1:8888"
	DefaultDatabaseURL = "sqlite:///etc/olcpanel/panel.db"
)

type Config struct {
	BindAddress string
	DatabaseURL string
}

type LoadOptions struct {
	BindAddress string
	DatabaseURL string
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

	return Config{
		BindAddress: bindAddress,
		DatabaseURL: databaseURL,
	}, nil
}

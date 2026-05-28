package config

import "os"

const DefaultBindAddress = "127.0.0.1:8888"

type Config struct {
	BindAddress string
}

func Load() (Config, error) {
	bindAddress := os.Getenv("OLCPANEL_BIND")
	if bindAddress == "" {
		bindAddress = DefaultBindAddress
	}

	return Config{BindAddress: bindAddress}, nil
}

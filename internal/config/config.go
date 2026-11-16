// Package config loads service settings from environment variables.
package config

import (
	"fmt"
	"log/slog"
	"time"
)

// Config holds every setting the API needs at startup.
type Config struct {
	// Addr is the host:port the HTTP server listens on.
	Addr string
	// LogLevel is the minimum level that gets logged.
	LogLevel slog.Level
	// ShutdownTimeout bounds how long in-flight requests get to finish on shutdown.
	ShutdownTimeout time.Duration
}

// Defaults are safe for local development on the Pi: the server only listens on loopback.
const (
	defaultAddr            = "127.0.0.1:8090"
	defaultLogLevel        = "info"
	defaultShutdownTimeout = 10 * time.Second
)

// Load reads settings through getenv (pass os.Getenv in production, a fake in tests).
func Load(getenv func(string) string) (Config, error) {
	cfg := Config{
		Addr:            defaultAddr,
		ShutdownTimeout: defaultShutdownTimeout,
	}

	if v := getenv("JOBTRACK_ADDR"); v != "" {
		cfg.Addr = v
	}

	level := defaultLogLevel
	if v := getenv("JOBTRACK_LOG_LEVEL"); v != "" {
		level = v
	}
	if err := cfg.LogLevel.UnmarshalText([]byte(level)); err != nil {
		return Config{}, fmt.Errorf("JOBTRACK_LOG_LEVEL: %w", err)
	}

	if v := getenv("JOBTRACK_SHUTDOWN_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("JOBTRACK_SHUTDOWN_TIMEOUT: %w", err)
		}
		if d <= 0 {
			return Config{}, fmt.Errorf("JOBTRACK_SHUTDOWN_TIMEOUT: must be positive, got %s", d)
		}
		cfg.ShutdownTimeout = d
	}

	return cfg, nil
}

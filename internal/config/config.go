// Package config loads service settings from environment variables.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
)

// Config holds every setting the API needs at startup.
type Config struct {
	// Addr is the host:port the HTTP server listens on.
	Addr string
	// DatabaseURL is the Postgres connection string (required).
	DatabaseURL string
	// LogLevel is the minimum level that gets logged.
	LogLevel slog.Level
	// ShutdownTimeout bounds how long in-flight requests get to finish on shutdown.
	ShutdownTimeout time.Duration

	// NtfyURL is the ntfy topic reminders go to (e.g. https://ntfy.sh/<long random topic>).
	// Empty means notifications are only logged.
	NtfyURL string
	// NtfyToken is an optional access token for a protected topic.
	NtfyToken string
	// ReminderAfterDays: remind to follow up after this many days without a stage change.
	ReminderAfterDays int
	// Reminders fire only from ReminderStartHour to ReminderEndHour (exclusive), local
	// time (set TZ). Configured as JOBTRACK_REMINDER_HOURS=9-21; 0-24 means any time.
	ReminderStartHour, ReminderEndHour int
}

// Defaults are safe for local development on the Pi: the server only listens on loopback.
const (
	defaultAddr              = "127.0.0.1:8090"
	defaultLogLevel          = "info"
	defaultShutdownTimeout   = 10 * time.Second
	defaultReminderAfterDays = 7
	defaultReminderHours     = "9-21"
)

// Load reads settings through getenv (pass os.Getenv in production, a fake in tests).
func Load(getenv func(string) string) (Config, error) {
	cfg := Config{
		Addr:              defaultAddr,
		ShutdownTimeout:   defaultShutdownTimeout,
		ReminderAfterDays: defaultReminderAfterDays,
		NtfyURL:           getenv("JOBTRACK_NTFY_URL"),
		NtfyToken:         getenv("JOBTRACK_NTFY_TOKEN"),
	}

	cfg.DatabaseURL = getenv("JOBTRACK_DATABASE_URL")
	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("JOBTRACK_DATABASE_URL is required")
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

	if v := getenv("JOBTRACK_REMINDER_AFTER_DAYS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 365 {
			return Config{}, fmt.Errorf("JOBTRACK_REMINDER_AFTER_DAYS: must be a whole number of days from 1 to 365, got %q", v)
		}
		cfg.ReminderAfterDays = n
	}

	hours := defaultReminderHours
	if v := getenv("JOBTRACK_REMINDER_HOURS"); v != "" {
		hours = v
	}
	start, end, err := parseHours(hours)
	if err != nil {
		return Config{}, fmt.Errorf("JOBTRACK_REMINDER_HOURS: %w", err)
	}
	cfg.ReminderStartHour, cfg.ReminderEndHour = start, end

	return cfg, nil
}

// parseHours parses "START-END" with 0 <= START < END <= 24.
func parseHours(s string) (start, end int, err error) {
	a, b, ok := strings.Cut(s, "-")
	if ok {
		start, err = strconv.Atoi(strings.TrimSpace(a))
		if err == nil {
			end, err = strconv.Atoi(strings.TrimSpace(b))
		}
	}
	if !ok || err != nil || start < 0 || end > 24 || start >= end {
		return 0, 0, fmt.Errorf("want START-END hours like 9-21 (0 <= START < END <= 24), got %q", s)
	}
	return start, end, nil
}

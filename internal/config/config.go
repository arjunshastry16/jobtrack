// Package config loads service settings from environment variables.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"time"
)

// Config holds every setting the API needs at startup.
type Config struct {
	// Addr is the host:port the HTTP server listens on.
	Addr string
	// MetricsAddr is the host:port that serves Prometheus metrics (GET /metrics), apart
	// from the app so tailscale serve never exposes it and it needs no login. Empty (the
	// default) means no metrics listener.
	MetricsAddr string
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
	// SyncHour is the local hour (0-23) from which watched job boards are fetched each
	// day; the fetch happens once a day, in the first hourly check at or after it.
	SyncHour int

	// AllowedLogins are the Tailscale logins (lower case, e.g. "you@github") allowed to
	// use the app; tailscale serve passes the caller's login in a header. Required unless
	// AuthOff. Configured as a comma-separated JOBTRACK_ALLOWED_LOGINS.
	AllowedLogins []string
	// AuthOff lets every request through without a login (JOBTRACK_AUTH=off). For local
	// development only, so it's refused unless Addr is a loopback address.
	AuthOff bool
}

// DefaultAddr is where the server listens unless JOBTRACK_ADDR says otherwise: loopback
// only, for local development on the Pi. Production (the container image) listens on
// :8090, published on the Pi's 127.0.0.1:8090, so the two never collide.
const DefaultAddr = "127.0.0.1:8091"

// Other defaults.
const (
	defaultLogLevel          = "info"
	defaultShutdownTimeout   = 10 * time.Second
	defaultReminderAfterDays = 7
	defaultReminderHours     = "9-21"
	defaultSyncHour          = 6
)

// Load reads settings through getenv (pass os.Getenv in production, a fake in tests).
func Load(getenv func(string) string) (Config, error) {
	cfg := Config{
		Addr:              DefaultAddr,
		ShutdownTimeout:   defaultShutdownTimeout,
		ReminderAfterDays: defaultReminderAfterDays,
		SyncHour:          defaultSyncHour,
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

	if v := getenv("JOBTRACK_METRICS_ADDR"); v != "" {
		_, port, err := net.SplitHostPort(v)
		if err != nil {
			return Config{}, fmt.Errorf("JOBTRACK_METRICS_ADDR: want host:port, got %q", v)
		}
		if v == cfg.Addr && port != "0" { // port 0 means "any free port", so both can have it
			return Config{}, fmt.Errorf("JOBTRACK_METRICS_ADDR must differ from JOBTRACK_ADDR (both %q)", v)
		}
		cfg.MetricsAddr = v
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

	if v := getenv("JOBTRACK_SYNC_HOUR"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > 23 {
			return Config{}, fmt.Errorf("JOBTRACK_SYNC_HOUR: must be an hour from 0 to 23, got %q", v)
		}
		cfg.SyncHour = n
	}

	if err := loadAuth(&cfg, getenv); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

// loadAuth reads who may use the app. It fails closed: no allowlist is an error, not
// "anyone".
func loadAuth(cfg *Config, getenv func(string) string) error {
	switch mode := getenv("JOBTRACK_AUTH"); mode {
	case "", "tailscale":
	case "off":
		if !isLoopback(cfg.Addr) {
			return fmt.Errorf("JOBTRACK_AUTH=off is only allowed on a loopback address, but JOBTRACK_ADDR is %q", cfg.Addr)
		}
		cfg.AuthOff = true
		return nil
	default:
		return fmt.Errorf(`JOBTRACK_AUTH: want "tailscale" or "off", got %q`, mode)
	}

	for l := range strings.SplitSeq(getenv("JOBTRACK_ALLOWED_LOGINS"), ",") {
		l = strings.ToLower(strings.TrimSpace(l))
		if l == "" {
			continue
		}
		// Tailscale logins always have an @ (you@github, you@example.com); a display
		// name here would never match, so it's caught now rather than as a 403 later.
		if !strings.Contains(l, "@") || strings.ContainsAny(l, " \t") {
			return fmt.Errorf("JOBTRACK_ALLOWED_LOGINS: %q doesn't look like a Tailscale login (e.g. you@github)", l)
		}
		cfg.AllowedLogins = append(cfg.AllowedLogins, l)
	}
	if len(cfg.AllowedLogins) == 0 {
		return errors.New("JOBTRACK_ALLOWED_LOGINS is required: the Tailscale logins allowed to use the app " +
			"(or JOBTRACK_AUTH=off for local development)")
	}
	return nil
}

// isLoopback reports whether addr (host:port) only listens on this machine.
func isLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
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

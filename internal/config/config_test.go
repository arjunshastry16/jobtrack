package config

import (
	"log/slog"
	"maps"
	"reflect"
	"testing"
	"time"
)

// fakeEnv turns a map into a getenv-style function.
func fakeEnv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

const testURL = "postgres://u@localhost:5432/db"

// env is a minimal valid environment plus extra settings.
func env(extra map[string]string) map[string]string {
	m := map[string]string{"JOBTRACK_DATABASE_URL": testURL, "JOBTRACK_ALLOWED_LOGINS": "me@github"}
	maps.Copy(m, extra)
	return m
}

func TestLoad(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		want    Config
		wantErr bool
	}{
		{
			name: "defaults",
			env:  env(nil),
			want: Config{
				Addr: "127.0.0.1:8091", DatabaseURL: testURL, LogLevel: slog.LevelInfo, ShutdownTimeout: 10 * time.Second,
				ReminderAfterDays: 7, ReminderStartHour: 9, ReminderEndHour: 21, SyncHour: 6,
				AllowedLogins: []string{"me@github"},
			},
		},
		{
			name: "overrides",
			env: map[string]string{
				"JOBTRACK_DATABASE_URL":        testURL,
				"JOBTRACK_ADDR":                ":8080",
				"JOBTRACK_LOG_LEVEL":           "debug",
				"JOBTRACK_SHUTDOWN_TIMEOUT":    "3s",
				"JOBTRACK_NTFY_URL":            "https://ntfy.example/jt",
				"JOBTRACK_NTFY_TOKEN":          "tk_1",
				"JOBTRACK_REMINDER_AFTER_DAYS": "10",
				"JOBTRACK_REMINDER_HOURS":      " 0 - 24 ",
				"JOBTRACK_SYNC_HOUR":           "0",
				"JOBTRACK_AUTH":                "tailscale",
				"JOBTRACK_ALLOWED_LOGINS":      " Me@GitHub, ,friend@example.com ",
			},
			want: Config{
				Addr: ":8080", DatabaseURL: testURL, LogLevel: slog.LevelDebug, ShutdownTimeout: 3 * time.Second,
				NtfyURL: "https://ntfy.example/jt", NtfyToken: "tk_1",
				ReminderAfterDays: 10, ReminderStartHour: 0, ReminderEndHour: 24, SyncHour: 0,
				AllowedLogins: []string{"me@github", "friend@example.com"},
			},
		},
		{
			name: "auth off on loopback",
			env:  map[string]string{"JOBTRACK_DATABASE_URL": testURL, "JOBTRACK_AUTH": "off", "JOBTRACK_ADDR": "[::1]:9000"},
			want: Config{
				Addr: "[::1]:9000", DatabaseURL: testURL, LogLevel: slog.LevelInfo, ShutdownTimeout: 10 * time.Second,
				ReminderAfterDays: 7, ReminderStartHour: 9, ReminderEndHour: 21, SyncHour: 6, AuthOff: true,
			},
		},
		{
			name: "auth off on localhost",
			env:  map[string]string{"JOBTRACK_DATABASE_URL": testURL, "JOBTRACK_AUTH": "off", "JOBTRACK_ADDR": "localhost:8090"},
			want: Config{
				Addr: "localhost:8090", DatabaseURL: testURL, LogLevel: slog.LevelInfo, ShutdownTimeout: 10 * time.Second,
				ReminderAfterDays: 7, ReminderStartHour: 9, ReminderEndHour: 21, SyncHour: 6, AuthOff: true,
			},
		},
		{name: "no allowed logins", env: map[string]string{"JOBTRACK_DATABASE_URL": testURL}, wantErr: true},
		{name: "only blank logins", env: env(map[string]string{"JOBTRACK_ALLOWED_LOGINS": " , "}), wantErr: true},
		{name: "login without @", env: env(map[string]string{"JOBTRACK_ALLOWED_LOGINS": "me@github,Arjun"}), wantErr: true},
		{name: "login with a space", env: env(map[string]string{"JOBTRACK_ALLOWED_LOGINS": "me @github"}), wantErr: true},
		{name: "unknown auth mode", env: env(map[string]string{"JOBTRACK_AUTH": "none"}), wantErr: true},
		{name: "auth off on all interfaces", env: env(map[string]string{"JOBTRACK_AUTH": "off", "JOBTRACK_ADDR": ":8090"}), wantErr: true},
		{name: "auth off on a LAN address", env: env(map[string]string{"JOBTRACK_AUTH": "off", "JOBTRACK_ADDR": "192.168.1.5:8090"}), wantErr: true},
		{name: "missing database url", env: map[string]string{}, wantErr: true},
		{name: "bad log level", env: env(map[string]string{"JOBTRACK_LOG_LEVEL": "loud"}), wantErr: true},
		{name: "bad duration", env: env(map[string]string{"JOBTRACK_SHUTDOWN_TIMEOUT": "soon"}), wantErr: true},
		{name: "non-positive duration", env: env(map[string]string{"JOBTRACK_SHUTDOWN_TIMEOUT": "0s"}), wantErr: true},
		{name: "zero reminder days", env: env(map[string]string{"JOBTRACK_REMINDER_AFTER_DAYS": "0"}), wantErr: true},
		{name: "reminder days not a number", env: env(map[string]string{"JOBTRACK_REMINDER_AFTER_DAYS": "week"}), wantErr: true},
		{name: "hours reversed", env: env(map[string]string{"JOBTRACK_REMINDER_HOURS": "21-9"}), wantErr: true},
		{name: "hours past 24", env: env(map[string]string{"JOBTRACK_REMINDER_HOURS": "9-25"}), wantErr: true},
		{name: "hours not a range", env: env(map[string]string{"JOBTRACK_REMINDER_HOURS": "9am"}), wantErr: true},
		{name: "sync hour 24", env: env(map[string]string{"JOBTRACK_SYNC_HOUR": "24"}), wantErr: true},
		{name: "sync hour not a number", env: env(map[string]string{"JOBTRACK_SYNC_HOUR": "6am"}), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Load(fakeEnv(tt.env))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Load() error = nil, want an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Load() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

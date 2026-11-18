package config

import (
	"log/slog"
	"testing"
	"time"
)

// fakeEnv turns a map into a getenv-style function.
func fakeEnv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

const testURL = "postgres://u@localhost:5432/db"

func TestLoad(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		want    Config
		wantErr bool
	}{
		{
			name: "defaults",
			env:  map[string]string{"JOBTRACK_DATABASE_URL": testURL},
			want: Config{
				Addr: "127.0.0.1:8090", DatabaseURL: testURL, LogLevel: slog.LevelInfo, ShutdownTimeout: 10 * time.Second,
				ReminderAfterDays: 7, ReminderStartHour: 9, ReminderEndHour: 21, SyncHour: 6,
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
			},
			want: Config{
				Addr: ":8080", DatabaseURL: testURL, LogLevel: slog.LevelDebug, ShutdownTimeout: 3 * time.Second,
				NtfyURL: "https://ntfy.example/jt", NtfyToken: "tk_1",
				ReminderAfterDays: 10, ReminderStartHour: 0, ReminderEndHour: 24, SyncHour: 0,
			},
		},
		{name: "missing database url", env: map[string]string{}, wantErr: true},
		{name: "bad log level", env: map[string]string{"JOBTRACK_DATABASE_URL": testURL, "JOBTRACK_LOG_LEVEL": "loud"}, wantErr: true},
		{name: "bad duration", env: map[string]string{"JOBTRACK_DATABASE_URL": testURL, "JOBTRACK_SHUTDOWN_TIMEOUT": "soon"}, wantErr: true},
		{name: "non-positive duration", env: map[string]string{"JOBTRACK_DATABASE_URL": testURL, "JOBTRACK_SHUTDOWN_TIMEOUT": "0s"}, wantErr: true},
		{name: "zero reminder days", env: map[string]string{"JOBTRACK_DATABASE_URL": testURL, "JOBTRACK_REMINDER_AFTER_DAYS": "0"}, wantErr: true},
		{name: "reminder days not a number", env: map[string]string{"JOBTRACK_DATABASE_URL": testURL, "JOBTRACK_REMINDER_AFTER_DAYS": "week"}, wantErr: true},
		{name: "hours reversed", env: map[string]string{"JOBTRACK_DATABASE_URL": testURL, "JOBTRACK_REMINDER_HOURS": "21-9"}, wantErr: true},
		{name: "hours past 24", env: map[string]string{"JOBTRACK_DATABASE_URL": testURL, "JOBTRACK_REMINDER_HOURS": "9-25"}, wantErr: true},
		{name: "hours not a range", env: map[string]string{"JOBTRACK_DATABASE_URL": testURL, "JOBTRACK_REMINDER_HOURS": "9am"}, wantErr: true},
		{name: "sync hour 24", env: map[string]string{"JOBTRACK_DATABASE_URL": testURL, "JOBTRACK_SYNC_HOUR": "24"}, wantErr: true},
		{name: "sync hour not a number", env: map[string]string{"JOBTRACK_DATABASE_URL": testURL, "JOBTRACK_SYNC_HOUR": "6am"}, wantErr: true},
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
			if got != tt.want {
				t.Errorf("Load() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

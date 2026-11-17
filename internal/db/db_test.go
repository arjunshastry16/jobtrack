package db

import (
	"testing"
	"time"
)

func TestOpenConnectTimeout(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want time.Duration
	}{
		{name: "default when unset", url: "postgres://u@127.0.0.1:5432/db", want: 5 * time.Second},
		{name: "respects connect_timeout in URL", url: "postgres://u@127.0.0.1:5432/db?connect_timeout=1", want: time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool, err := Open(t.Context(), tt.url) // lazy: does not connect
			if err != nil {
				t.Fatalf("Open() error: %v", err)
			}
			defer pool.Close()
			if got := pool.Config().ConnConfig.ConnectTimeout; got != tt.want {
				t.Errorf("ConnectTimeout = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestOpenRejectsBadURL(t *testing.T) {
	if _, err := Open(t.Context(), "not a url ::"); err == nil {
		t.Fatal("Open() error = nil, want a parse error")
	}
}

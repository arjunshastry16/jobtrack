package requestid

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestNewIsValidAndUnique(t *testing.T) {
	a, b := New(), New()
	if !Valid(a) || len(a) != 16 {
		t.Errorf("New() = %q, want 16 valid hex chars", a)
	}
	if a == b {
		t.Errorf("New() returned the same ID twice: %q", a)
	}
}

func TestValid(t *testing.T) {
	tests := map[string]bool{
		"abc-123_X.y":             true,
		"":                        false,
		strings.Repeat("a", 64):   true,
		strings.Repeat("a", 65):   false,
		"has space":               false,
		"new\nline":               false,
		`quote"`:                  false,
		"ünicode":                 false,
		"0f3c9a7b1d2e4f60":        true,
		"550e8400-e29b-41d4-a716": true,
	}
	for in, want := range tests {
		if got := Valid(in); got != want {
			t.Errorf("Valid(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestLogHandlerAddsRequestID(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(LogHandler{slog.NewJSONHandler(&buf, nil)}).With("svc", "test")

	logger.InfoContext(NewContext(t.Context(), "req-1"), "with id")
	logger.InfoContext(t.Context(), "without id")

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d log lines, want 2:\n%s", len(lines), buf.String())
	}
	var first, second map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatal(err)
	}
	if first["request_id"] != "req-1" || first["svc"] != "test" {
		t.Errorf("first line = %v, want request_id=req-1 and svc=test (wrapper kept by With)", first)
	}
	if _, ok := second["request_id"]; ok {
		t.Errorf("second line has request_id but context had none: %v", second)
	}
}

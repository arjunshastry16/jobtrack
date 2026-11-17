// Package requestid carries a per-request ID through context.Context and adds it to
// every log line written with that context, so all logs for one request can be found
// by searching for its ID.
package requestid

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
)

// Header is the HTTP header the ID is read from and returned in.
const Header = "X-Request-ID"

const maxLen = 64

type ctxKey struct{} // unexported type, so no other package can collide with this key

// NewContext returns a copy of ctx that carries id.
func NewContext(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// FromContext returns the request ID in ctx, or "" if there is none.
func FromContext(ctx context.Context) string {
	id, _ := ctx.Value(ctxKey{}).(string)
	return id
}

// New returns a random 16-character hex ID.
func New() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b) // crypto/rand.Read never returns an error on supported platforms
	return hex.EncodeToString(b)
}

// Valid reports whether an incoming ID is safe to reuse: short, and only characters
// that can't break log formats or headers.
func Valid(id string) bool {
	if id == "" || len(id) > maxLen {
		return false
	}
	for _, c := range id {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_', c == '.':
		default:
			return false
		}
	}
	return true
}

// LogHandler wraps a slog.Handler and adds a request_id attribute to every record
// logged with a context that carries an ID (logger.InfoContext(ctx, ...) etc.).
type LogHandler struct {
	slog.Handler
}

// Handle adds request_id (if present in ctx) and passes the record on.
func (h LogHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := FromContext(ctx); id != "" {
		r.AddAttrs(slog.String("request_id", id))
	}
	return h.Handler.Handle(ctx, r)
}

// WithAttrs keeps the wrapper when attributes are added (logger.With(...)).
func (h LogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return LogHandler{h.Handler.WithAttrs(attrs)}
}

// WithGroup keeps the wrapper when a group is added (logger.WithGroup(...)).
func (h LogHandler) WithGroup(name string) slog.Handler {
	return LogHandler{h.Handler.WithGroup(name)}
}

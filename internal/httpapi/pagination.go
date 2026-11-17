package httpapi

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Keyset ("cursor") pagination: instead of ?page=3 (OFFSET), the client sends back an
// opaque cursor naming the last row it saw, and the next page is "rows after that one".
// It stays fast on large tables (no scanning past skipped rows) and doesn't skip or
// repeat rows when new ones are inserted between page requests.

const (
	defaultPageSize = 50
	maxPageSize     = 200
	cursorPrefix    = "v1:" // versioned so the cursor format can change without breaking old ones silently
)

// parsePageSize reads the ?limit= value; empty means the default.
func parsePageSize(s string) (int, error) {
	if s == "" {
		return defaultPageSize, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > maxPageSize {
		return 0, fmt.Errorf("must be an integer from 1 to %d", maxPageSize)
	}
	return n, nil
}

// encodeCursor makes an opaque cursor from the last row's id. Clients must not parse it.
func encodeCursor(id int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(cursorPrefix + strconv.FormatInt(id, 10)))
}

var errBadCursor = errors.New("is not a valid cursor")

// decodeCursor reverses encodeCursor.
func decodeCursor(s string) (int64, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return 0, errBadCursor
	}
	rest, ok := strings.CutPrefix(string(raw), cursorPrefix)
	if !ok {
		return 0, errBadCursor
	}
	id, err := strconv.ParseInt(rest, 10, 64)
	if err != nil || id <= 0 {
		return 0, errBadCursor
	}
	return id, nil
}

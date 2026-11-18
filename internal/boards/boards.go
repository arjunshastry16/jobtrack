// Package boards fetches job postings from companies' public job boards (applicant
// tracking systems such as Greenhouse). Each board type implements Board; callers don't
// care which ATS a company uses.
//
// Official public JSON APIs are used, never HTML scraping. The one exception is Workday,
// which has no public API: its client reads the JSON the public careers page itself loads,
// and is deliberately slow and polite (see Workday).
package boards

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Posting is one open job, normalised across boards.
type Posting struct {
	ExternalID  string // the board's own id for the posting (unique per board+token)
	Title       string
	Location    string    // free text, e.g. "Remote, United States"; may be empty
	URL         string    // public page for the posting
	PublishedAt time.Time // zero if the board doesn't say
	UpdatedAt   time.Time // zero if the board doesn't say
}

// Board fetches every currently open posting for one company on one job board.
type Board interface {
	// Name is the board type as stored in watches.board, e.g. "greenhouse".
	Name() string
	// Fetch returns the open postings for the company identified by token.
	Fetch(ctx context.Context, token string) ([]Posting, error)
}

// Supported lists the board types that have a client, i.e. what a watch may use.
var Supported = []string{"greenhouse", "lever", "ashby", "smartrecruiters", "workday"}

// All returns a client for every supported board, keyed by name, all using client (nil
// gets a default with a 30s timeout). Create it once and share it: the Workday client's
// pacing only holds within one value.
func All(client *http.Client) map[string]Board {
	return map[string]Board{
		"greenhouse":      NewGreenhouse(client),
		"lever":           NewLever(client),
		"ashby":           NewAshby(client),
		"smartrecruiters": NewSmartRecruiters(client),
		"workday":         NewWorkday(client),
	}
}

// ErrBoardNotFound means the board has no company with that token (HTTP 404). It is not
// worth retrying: the watch is misconfigured.
var ErrBoardNotFound = errors.New("boards: no such company board")

// HTTPError is an unexpected HTTP status from a board's API.
type HTTPError struct {
	Board string
	Code  int
	// RetryAfter is how long the board asked us to wait (Retry-After header), or 0.
	RetryAfter time.Duration
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("boards: %s returned HTTP %d", e.Board, e.Code)
}

// tokenPattern matches the company tokens boards use in their URLs. Validating before
// building a URL keeps odd input (slashes, "..", spaces) out of requests.
var tokenPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)

// ValidToken reports whether s looks like a board token. Must match the CHECK
// constraint on watches.board_token. (Workday tokens are different: see ParseWorkdayURL.)
func ValidToken(s string) bool {
	return tokenPattern.MatchString(s) && s != "." && s != ".."
}

// NormalizeToken checks what a user entered as a watch's board token and returns the
// form to store: the company's id on the board, or for Workday the canonical form of
// the pasted careers-site URL.
func NormalizeToken(board, raw string) (string, error) {
	if board == "workday" {
		site, err := ParseWorkdayURL(raw)
		if err != nil {
			return "", err
		}
		return site.Token(), nil
	}
	tok := strings.TrimSpace(raw)
	if !ValidToken(tok) {
		return "", errors.New(`must be the company's id on the board, e.g. "stripe" (letters, digits, . _ -)`)
	}
	return tok, nil
}

const (
	defaultTimeout = 30 * time.Second
	// maxResponseBytes caps a board response; big companies list a few hundred postings
	// (~200 KB), so this is generous while still bounding memory.
	maxResponseBytes = 20 << 20
	userAgent        = "jobtrack/0.1"
)

func defaultClient(c *http.Client) *http.Client {
	if c != nil {
		return c
	}
	return &http.Client{Timeout: defaultTimeout}
}

// getJSON fetches url and returns the (size-limited) body if the status is 200.
func getJSON(ctx context.Context, client *http.Client, board, url string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("boards: %s: build request: %w", board, err)
	}
	return doJSON(client, board, req)
}

// doJSON sends req and returns the (size-limited) body if the status is 200; a 404 is
// ErrBoardNotFound and anything else an *HTTPError.
func doJSON(client *http.Client, board string, req *http.Request) (io.ReadCloser, error) {
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("boards: %s: %w", board, err)
	}
	switch resp.StatusCode {
	case http.StatusOK:
		return struct {
			io.Reader
			io.Closer
		}{io.LimitReader(resp.Body, maxResponseBytes), resp.Body}, nil
	case http.StatusNotFound:
		_ = resp.Body.Close()
		return nil, ErrBoardNotFound
	default:
		_ = resp.Body.Close()
		return nil, &HTTPError{Board: board, Code: resp.StatusCode, RetryAfter: retryAfter(resp.Header.Get("Retry-After"), time.Now())}
	}
}

// retryAfter parses a Retry-After header: delay-seconds or an HTTP date. 0 if absent or invalid.
func retryAfter(h string, now time.Time) time.Duration {
	h = strings.TrimSpace(h)
	if h == "" {
		return 0
	}
	if secs, err := strconv.Atoi(h); err == nil {
		return max(time.Duration(secs)*time.Second, 0)
	}
	if t, err := http.ParseTime(h); err == nil {
		return max(t.Sub(now), 0)
	}
	return 0
}

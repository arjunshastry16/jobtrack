package boards

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Workday reads a company's Workday careers site.
//
// UNOFFICIAL: Workday has no public jobs API. This client calls the same JSON endpoint
// that the public careers page (https://{tenant}.wd{N}.myworkdayjobs.com/{site}) loads in
// the browser:
//
//	POST https://{tenant}.wd{N}.myworkdayjobs.com/wday/cxs/{tenant}/{site}/jobs
//	{"appliedFacets": {...}, "limit": 20, "offset": 0, "searchText": "..."}
//
// It may break whenever Workday changes its site, and it is deliberately polite:
//   - One request at a time across every Workday site, at least wdMinGap apart.
//   - HTTP 429 is honoured: wait for Retry-After (a minute if absent, at most five) and try
//     again, up to wdMaxAttempts in all; then give up until the next run.
//   - It fetches only what the watch's URL is filtered to (e.g. one country). A site with
//     more than wdMaxPages pages is refused up front rather than paged through.
//   - It is meant for the nightly sync only.
//   - Nothing tries to get past bot protection: plain requests with an honest User-Agent.
//     If Workday blocks them, the fetch fails with that status.
type Workday struct {
	baseURL  string // "" means https://{host}; tests point every site at one server
	client   *http.Client
	now      func() time.Time
	minGap   time.Duration
	pageSize int
	maxPages int
	// How long to wait after a 429: the default when there's no Retry-After, and the cap.
	retryDefault, retryCap time.Duration

	// slot holds the one request in flight; last (guarded by slot) is when it ended.
	slot chan struct{}
	last time.Time
}

const (
	wdMinGap      = 5 * time.Second
	wdPageSize    = 20 // the endpoint rejects anything larger
	wdMaxPages    = 40 // 800 postings, about 3.5 minutes at wdMinGap
	wdMaxAttempts = 3
)

// NewWorkday returns a Workday client. A nil client gets a default with a 30s timeout.
// Share one Workday value between fetches: the politeness limits are per value.
func NewWorkday(client *http.Client) *Workday { return NewWorkdayAt("", client) }

// NewWorkdayAt is NewWorkday with every site's requests sent to baseURL (for tests).
func NewWorkdayAt(baseURL string, client *http.Client) *Workday {
	return &Workday{
		baseURL: baseURL, client: defaultClient(client), now: time.Now,
		minGap: wdMinGap, pageSize: wdPageSize, maxPages: wdMaxPages,
		retryDefault: time.Minute, retryCap: 5 * time.Minute,
		slot: make(chan struct{}, 1),
	}
}

// Name implements Board.
func (w *Workday) Name() string { return "workday" }

type wdRequest struct {
	AppliedFacets map[string][]string `json:"appliedFacets"`
	Limit         int                 `json:"limit"`
	Offset        int                 `json:"offset"`
	SearchText    string              `json:"searchText"`
}

type wdResponse struct {
	// Total is only meaningful on the first page, and Workday caps it at 2000.
	Total       int `json:"total"`
	JobPostings []struct {
		Title         string `json:"title"`
		ExternalPath  string `json:"externalPath"`  // "/job/India-Bengaluru/Backend-Engineer_JR123"
		LocationsText string `json:"locationsText"` // "India, Bengaluru", or "2 Locations"
		PostedOn      string `json:"postedOn"`      // "Posted Today", "Posted 3 Days Ago"
	} `json:"jobPostings"`
}

// Fetch implements Board. token is a careers-site URL or its canonical form
// (see ParseWorkdayURL).
func (w *Workday) Fetch(ctx context.Context, token string) ([]Posting, error) {
	site, err := ParseWorkdayURL(token)
	if err != nil {
		return nil, fmt.Errorf("boards: workday: invalid site %q: %w", token, err)
	}
	var out []Posting
	seen := map[string]bool{}
	total := 0
	for page := range w.maxPages {
		resp, err := w.fetchPage(ctx, site, page*w.pageSize)
		if err != nil {
			return nil, err
		}
		if page == 0 {
			total = resp.Total
			if total > w.maxPages*w.pageSize {
				return nil, fmt.Errorf("boards: workday: %s lists %d+ postings; filter the careers page (e.g. by country) and watch that URL instead",
					site.Token(), total)
			}
		}
		for _, p := range resp.JobPostings {
			title := strings.TrimSpace(p.Title)
			if title == "" || !strings.HasPrefix(p.ExternalPath, "/") || seen[p.ExternalPath] {
				continue
			}
			seen[p.ExternalPath] = true
			out = append(out, Posting{
				ExternalID:  wdExternalID(p.ExternalPath),
				Title:       title,
				Location:    joinLocations(p.LocationsText),
				URL:         "https://" + site.Host + "/" + site.Site + p.ExternalPath,
				PublishedAt: wdPostedOn(p.PostedOn, w.now()),
			})
		}
		if len(resp.JobPostings) < w.pageSize || (page+1)*w.pageSize >= total {
			return out, nil
		}
	}
	return out, fmt.Errorf("boards: workday: %s has more than %d postings; stopped paging", site.Token(), w.maxPages*w.pageSize)
}

func (w *Workday) fetchPage(ctx context.Context, site WorkdaySite, offset int) (wdResponse, error) {
	facets := map[string][]string(site.Facets)
	if facets == nil {
		facets = map[string][]string{} // the endpoint wants {}, not null
	}
	body, err := json.Marshal(wdRequest{AppliedFacets: facets, Limit: w.pageSize, Offset: offset, SearchText: site.Search})
	if err != nil {
		return wdResponse{}, fmt.Errorf("boards: workday: encode request: %w", err)
	}
	base := w.baseURL
	if base == "" {
		base = "https://" + site.Host
	}
	// Tenant and Site were checked against strict patterns, so they're safe in a path.
	endpoint := base + "/wday/cxs/" + site.Tenant + "/" + site.Site + "/jobs"

	for attempt := 1; ; attempt++ {
		resp, err := w.post(ctx, endpoint, body)
		var he *HTTPError
		if errors.As(err, &he) && he.Code == http.StatusTooManyRequests && attempt < wdMaxAttempts {
			wait := he.RetryAfter
			if wait <= 0 {
				wait = w.retryDefault
			}
			if err := sleepCtx(ctx, min(wait, w.retryCap)); err != nil {
				return wdResponse{}, fmt.Errorf("boards: workday: waiting after 429: %w", err)
			}
			continue
		}
		return resp, err
	}
}

// post sends one request, waiting for its turn: one at a time, minGap apart.
func (w *Workday) post(ctx context.Context, endpoint string, body []byte) (wdResponse, error) {
	select {
	case w.slot <- struct{}{}:
	case <-ctx.Done():
		return wdResponse{}, fmt.Errorf("boards: workday: %w", ctx.Err())
	}
	defer func() {
		w.last = time.Now()
		<-w.slot
	}()
	if !w.last.IsZero() {
		if err := sleepCtx(ctx, w.minGap-time.Since(w.last)); err != nil {
			return wdResponse{}, fmt.Errorf("boards: workday: %w", err)
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return wdResponse{}, fmt.Errorf("boards: workday: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	rc, err := doJSON(w.client, w.Name(), req)
	var he *HTTPError
	if errors.As(err, &he) && he.Code == http.StatusUnprocessableEntity {
		return wdResponse{}, ErrBoardNotFound // what Workday answers for an unknown company
	}
	if err != nil {
		return wdResponse{}, err
	}
	defer func() { _ = rc.Close() }()
	var resp wdResponse
	if err := json.NewDecoder(rc).Decode(&resp); err != nil {
		return wdResponse{}, fmt.Errorf("boards: workday: decode: %w", err)
	}
	return resp, nil
}

// wdExternalID is the requisition id at the end of the job path ("..._JR2029119-1"), which
// stays the same if the title is edited; the whole path if there isn't one.
func wdExternalID(path string) string {
	last := path[strings.LastIndex(path, "/")+1:]
	if i := strings.LastIndex(last, "_"); i >= 0 && i < len(last)-1 {
		return last[i+1:]
	}
	return path
}

var wdDaysAgo = regexp.MustCompile(`^posted (\d{1,2}) days? ago$`)

// wdPostedOn turns Workday's relative "Posted 3 Days Ago" into a date (midnight UTC).
// "Posted 30+ Days Ago" and anything unrecognised give the zero time.
func wdPostedOn(s string, now time.Time) time.Time {
	now = now.UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	switch s = strings.ToLower(strings.Join(strings.Fields(s), " ")); s {
	case "posted today":
		return today
	case "posted yesterday":
		return today.AddDate(0, 0, -1)
	}
	if m := wdDaysAgo.FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[1])
		return today.AddDate(0, 0, -n)
	}
	return time.Time{}
}

// sleepCtx waits for d, or until ctx ends.
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

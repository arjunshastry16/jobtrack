package boards

// Internal tests (package boards) so they can shrink Workday's page size and delays.

import (
	_ "embed" // for //go:embed
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

//go:embed testdata/workday_jobs.json
var wdJobsJSON []byte

const wdTestSite = "https://examplecorp.wd1.myworkdayjobs.com/en-US/External?locationCountry=c4f78be1a8f14da0ab49ce1162348a5e&q=engineer"

// fastWorkday is a Workday client pointed at srv with test-sized delays.
func fastWorkday(srv *httptest.Server) *Workday {
	w := NewWorkdayAt(srv.URL, nil)
	w.minGap = 0
	w.retryDefault, w.retryCap = 10*time.Millisecond, 20*time.Millisecond
	return w
}

func TestWorkdayFetch(t *testing.T) {
	var got wdRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/wday/cxs/examplecorp/External/jobs" ||
			r.Header.Get("Content-Type") != "application/json" {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_, _ = w.Write(wdJobsJSON)
	}))
	defer srv.Close()

	wd := fastWorkday(srv)
	wd.now = func() time.Time { return time.Date(2026, 11, 17, 20, 30, 0, 0, time.FixedZone("IST", 5*3600+1800)) }
	postings, err := wd.Fetch(t.Context(), wdTestSite)
	if err != nil {
		t.Fatal(err)
	}

	// The site's filters and search text are sent with the request.
	if got.Limit != 20 || got.Offset != 0 || got.SearchText != "engineer" ||
		!slices.Equal(got.AppliedFacets["locationCountry"], []string{"c4f78be1a8f14da0ab49ce1162348a5e"}) {
		t.Errorf("request = %+v", got)
	}

	// Untitled and duplicate postings are skipped.
	if len(postings) != 3 {
		t.Fatalf("got %d postings, want 3: %+v", len(postings), postings)
	}
	today := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	want := []Posting{
		{
			ExternalID: "JR1000001", Title: "Senior Backend Engineer", Location: "India, Bengaluru",
			URL:         "https://examplecorp.wd1.myworkdayjobs.com/External/job/India-Bengaluru/Senior-Backend-Engineer_JR1000001",
			PublishedAt: today,
		},
		{
			ExternalID: "JR1000002-1", Title: "Site Reliability Engineer", Location: "2 Locations",
			URL:         "https://examplecorp.wd1.myworkdayjobs.com/External/job/India-Pune/Site-Reliability-Engineer_JR1000002-1",
			PublishedAt: today.AddDate(0, 0, -3),
		},
		{
			ExternalID: "/job/India-Remote/Platform-Engineer", Title: "Platform Engineer", Location: "India, Remote",
			URL: "https://examplecorp.wd1.myworkdayjobs.com/External/job/India-Remote/Platform-Engineer",
		},
	}
	for i := range want {
		if postings[i] != want[i] {
			t.Errorf("posting %d =\n %+v\nwant\n %+v", i, postings[i], want[i])
		}
	}
}

// pagedServer serves n synthetic postings in pages, recording when each request arrived
// and how many were in flight at once.
type pagedServer struct {
	*httptest.Server
	mu       sync.Mutex
	starts   []time.Time
	inFlight atomic.Int32
	maxSeen  atomic.Int32
}

func newPagedServer(t *testing.T, n int) *pagedServer {
	ps := &pagedServer{}
	ps.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := ps.inFlight.Add(1)
		defer ps.inFlight.Add(-1)
		for {
			if m := ps.maxSeen.Load(); cur <= m || ps.maxSeen.CompareAndSwap(m, cur) {
				break
			}
		}
		ps.mu.Lock()
		ps.starts = append(ps.starts, time.Now())
		ps.mu.Unlock()

		var req wdRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		type job struct {
			Title        string `json:"title"`
			ExternalPath string `json:"externalPath"`
		}
		page := []job{}
		for i := req.Offset; i < min(req.Offset+req.Limit, n); i++ {
			page = append(page, job{fmt.Sprintf("Job %d", i), fmt.Sprintf("/job/X/Job_R%d", i)})
		}
		time.Sleep(5 * time.Millisecond) // long enough for overlapping requests to be seen
		_ = json.NewEncoder(w).Encode(map[string]any{"total": n, "jobPostings": page})
	}))
	t.Cleanup(ps.Close)
	return ps
}

func TestWorkdayPagesOneRequestAtATime(t *testing.T) {
	ps := newPagedServer(t, 5)
	wd := fastWorkday(ps.Server)
	wd.pageSize = 2
	wd.minGap = 30 * time.Millisecond

	// Two watches on the same client fetch at once; their requests must still be queued.
	var wg sync.WaitGroup
	for _, site := range []string{"a.wd1.myworkdayjobs.com/External", "b.wd1.myworkdayjobs.com/External"} {
		wg.Go(func() {
			got, err := wd.Fetch(t.Context(), site)
			if err != nil || len(got) != 5 {
				t.Errorf("%s: %d postings, err %v", site, len(got), err)
			}
		})
	}
	wg.Wait()

	if len(ps.starts) != 6 { // 3 pages (2, 2, 1) for each site
		t.Errorf("%d requests, want 6", len(ps.starts))
	}
	if m := ps.maxSeen.Load(); m != 1 {
		t.Errorf("%d requests in flight at once, want 1", m)
	}
	slices.SortFunc(ps.starts, time.Time.Compare)
	for i := 1; i < len(ps.starts); i++ {
		if gap := ps.starts[i].Sub(ps.starts[i-1]); gap < wd.minGap {
			t.Errorf("request %d started %s after the previous one, want >= %s", i, gap, wd.minGap)
		}
	}
}

func TestWorkdayHonours429(t *testing.T) {
	var requests atomic.Int32
	fail := atomic.Int32{} // how many 429s to send before succeeding
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) <= fail.Load() {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{"total":0,"jobPostings":[]}`))
	}))
	defer srv.Close()

	// One 429, then success: retried after the (capped) Retry-After.
	fail.Store(1)
	if _, err := fastWorkday(srv).Fetch(t.Context(), wdTestSite); err != nil || requests.Load() != 2 {
		t.Fatalf("after one 429: err %v, %d requests", err, requests.Load())
	}

	// Always 429: gives up after wdMaxAttempts and reports the status and Retry-After.
	requests.Store(0)
	fail.Store(100)
	_, err := fastWorkday(srv).Fetch(t.Context(), wdTestSite)
	var he *HTTPError
	if !errors.As(err, &he) || he.Code != http.StatusTooManyRequests || he.RetryAfter != time.Second {
		t.Fatalf("err = %v, want HTTPError 429 with RetryAfter 1s", err)
	}
	if n := requests.Load(); n != wdMaxAttempts {
		t.Errorf("%d requests, want %d", n, wdMaxAttempts)
	}
}

func TestWorkdayRefusesUnfilteredBigSite(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(`{"total":2000,"jobPostings":[{"title":"x","externalPath":"/job/x_1"}]}`))
	}))
	defer srv.Close()

	_, err := fastWorkday(srv).Fetch(t.Context(), "examplecorp.wd1.myworkdayjobs.com/External")
	if err == nil || !strings.Contains(err.Error(), "filter the careers page") {
		t.Fatalf("err = %v", err)
	}
	if requests.Load() != 1 {
		t.Errorf("%d requests, want 1 (refused after the first page)", requests.Load())
	}
}

func TestWorkdayUnknownCompany(t *testing.T) {
	// Workday answers 422 for a tenant that doesn't exist (404 is an unknown site).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
	}))
	defer srv.Close()
	if _, err := fastWorkday(srv).Fetch(t.Context(), wdTestSite); !errors.Is(err, ErrBoardNotFound) {
		t.Errorf("err = %v, want ErrBoardNotFound", err)
	}
}

func TestWdPostedOn(t *testing.T) {
	now := time.Date(2026, 11, 17, 2, 0, 0, 0, time.FixedZone("IST", 5*3600+1800)) // 16 Nov 20:30 UTC
	day := func(d int) time.Time { return time.Date(2026, 11, d, 0, 0, 0, 0, time.UTC) }
	tests := map[string]time.Time{
		"Posted Today":        day(16),
		"posted  yesterday":   day(15),
		"Posted 1 Day Ago":    day(15),
		"Posted 12 Days Ago":  day(4),
		"Posted 30+ Days Ago": {},
		"":                    {},
		"Posted 999 Days Ago": {},
	}
	for in, want := range tests {
		if got := wdPostedOn(in, now); !got.Equal(want) {
			t.Errorf("wdPostedOn(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestRetryAfter(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	tests := map[string]time.Duration{
		"":                              0,
		"120":                           2 * time.Minute,
		"-5":                            0,
		"soon":                          0,
		"Tue, 17 Nov 2026 12:00:30 GMT": 30 * time.Second,
		"Tue, 17 Nov 2026 11:00:00 GMT": 0, // in the past
	}
	for in, want := range tests {
		if got := retryAfter(in, now); got != want {
			t.Errorf("retryAfter(%q) = %s, want %s", in, got, want)
		}
	}
}

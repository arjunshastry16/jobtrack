package httpapi

import (
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"testing"

	"github.com/arjunshastry16/jobtrack/internal/store"
)

type postingPage struct {
	Postings   []postingJSON `json:"postings"`
	NextCursor *string       `json:"next_cursor"`
}

func mustCreateWatch(t *testing.T, h http.Handler, companyID int64, board, token string) int64 {
	t.Helper()
	var w watchJSON
	wantStatus(t, call(t, h, http.MethodPost, "/api/v1/watches", map[string]any{
		"company_id": companyID, "board": board, "board_token": token,
	}, &w), http.StatusCreated)
	return w.ID
}

// addPostings stores postings for a watch as the ingestion job would, one transaction
// each, so they get increasing ids. Returns the ids by external id.
func addPostings(t *testing.T, st *store.Store, watchID int64, externalIDs ...string) map[string]int64 {
	t.Helper()
	ids := map[string]int64{}
	for _, ext := range externalIDs {
		row, err := st.UpsertPosting(t.Context(), store.UpsertPostingParams{
			WatchID: watchID, ExternalID: ext, Title: "Engineer " + ext, Location: "Pune, India",
			Url: "https://jobs.example/" + ext,
		})
		if err != nil {
			t.Fatal(err)
		}
		ids[ext] = row.ID
	}
	return ids
}

// keepOnly re-runs a "fetch" that returns only these postings, so the watch's others
// are marked removed.
func keepOnly(t *testing.T, st *store.Store, watchID int64, externalIDs ...string) {
	t.Helper()
	err := st.InTx(t.Context(), func(q store.Querier) error {
		for _, ext := range externalIDs {
			if _, err := q.UpsertPosting(t.Context(), store.UpsertPostingParams{
				WatchID: watchID, ExternalID: ext, Title: "Engineer " + ext, Location: "Pune, India",
				Url: "https://jobs.example/" + ext,
			}); err != nil {
				return err
			}
		}
		_, err := q.MarkPostingsRemoved(t.Context(), watchID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func postingIDs(ps []postingJSON) []int64 {
	out := make([]int64, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.ID)
	}
	return out
}

func TestPostingsInbox(t *testing.T) {
	t.Parallel()
	h, st := newDBRouterAndStore(t)
	acme := mustCreateCompany(t, h, "Acme")
	globex := mustCreateCompany(t, h, "Globex")
	wa := mustCreateWatch(t, h, acme, "lever", "acme")
	wb := mustCreateWatch(t, h, globex, "greenhouse", "globex")

	a := addPostings(t, st, wa, "a1", "a2", "a3")
	b := addPostings(t, st, wb, "b1")
	keepOnly(t, st, wa, "a1", "a2") // a3 is no longer listed

	list := func(query string) []int64 {
		t.Helper()
		var page postingPage
		wantStatus(t, call(t, h, http.MethodGet, "/api/v1/postings"+query, nil, &page), http.StatusOK)
		return postingIDs(page.Postings)
	}

	// Current postings, newest first.
	if got, want := list(""), []int64{b["b1"], a["a2"], a["a1"]}; !slices.Equal(got, want) {
		t.Errorf("inbox = %v, want %v", got, want)
	}
	if got, want := list("?include_removed=true"), []int64{b["b1"], a["a3"], a["a2"], a["a1"]}; !slices.Equal(got, want) {
		t.Errorf("with removed = %v, want %v", got, want)
	}
	if got, want := list("?watch_id="+strconv.FormatInt(wa, 10)), []int64{a["a2"], a["a1"]}; !slices.Equal(got, want) {
		t.Errorf("watch filter = %v, want %v", got, want)
	}
	if got, want := list("?company_id="+strconv.FormatInt(globex, 10)), []int64{b["b1"]}; !slices.Equal(got, want) {
		t.Errorf("company filter = %v, want %v", got, want)
	}
	if got := list("?company_id=999999"); len(got) != 0 {
		t.Errorf("unknown company = %v, want none", got)
	}

	// The posting carries its company and board, and a removed one says when.
	var page postingPage
	wantStatus(t, call(t, h, http.MethodGet, "/api/v1/postings?include_removed=true&watch_id="+strconv.FormatInt(wa, 10), nil, &page), http.StatusOK)
	byID := map[int64]postingJSON{}
	for _, p := range page.Postings {
		byID[p.ID] = p
	}
	a1 := byID[a["a1"]]
	if a1.CompanyName != "Acme" || a1.CompanyID != acme || a1.Board != "lever" || a1.WatchID != wa ||
		a1.Title != "Engineer a1" || a1.URL != "https://jobs.example/a1" || a1.RemovedAt != nil {
		t.Errorf("a1 = %+v", a1)
	}
	if byID[a["a3"]].RemovedAt == nil {
		t.Errorf("a3 = %+v, want removed_at set", byID[a["a3"]])
	}
}

func TestPostingsPagination(t *testing.T) {
	t.Parallel()
	h, st := newDBRouterAndStore(t)
	w := mustCreateWatch(t, h, mustCreateCompany(t, h, "Acme"), "lever", "acme")
	const total = 7
	want := addPostings(t, st, w, "p1", "p2", "p3", "p4", "p5", "p6", "p7")

	seen := map[int64]bool{}
	var sizes []int
	lastID := int64(1 << 62)
	path := "/api/v1/postings?limit=3"
	for range total { // bounded: a pagination bug can't hang the test
		var page postingPage
		wantStatus(t, call(t, h, http.MethodGet, path, nil, &page), http.StatusOK)
		sizes = append(sizes, len(page.Postings))
		for _, p := range page.Postings {
			if seen[p.ID] || p.ID >= lastID {
				t.Fatalf("id %d repeated or out of order (after %d)", p.ID, lastID)
			}
			seen[p.ID], lastID = true, p.ID
		}
		if page.NextCursor == nil {
			break
		}
		path = "/api/v1/postings?limit=3&cursor=" + url.QueryEscape(*page.NextCursor)
	}
	if !slices.Equal(sizes, []int{3, 3, 1}) {
		t.Errorf("page sizes = %v, want [3 3 1]", sizes)
	}
	if !slices.Equal(slices.Sorted(maps.Keys(seen)), slices.Sorted(maps.Values(want))) {
		t.Errorf("saw %v, want %v", seen, want)
	}

	var page postingPage
	wantStatus(t, call(t, h, http.MethodGet, "/api/v1/postings?limit=7", nil, &page), http.StatusOK)
	if len(page.Postings) != total || page.NextCursor != nil {
		t.Errorf("limit=7: %d rows, next_cursor %v; want all rows and no cursor", len(page.Postings), page.NextCursor)
	}
}

func TestPostingsBadQuery(t *testing.T) {
	t.Parallel()
	h := newDBRouter(t)
	var body struct {
		Error struct {
			Code   string            `json:"code"`
			Fields map[string]string `json:"fields"`
		} `json:"error"`
	}
	rec := call(t, h, http.MethodGet,
		"/api/v1/postings?watch_id=0&company_id=x&include_removed=maybe&limit=500&cursor=junk", nil, &body)
	wantStatus(t, rec, http.StatusBadRequest)
	want := []string{"company_id", "cursor", "include_removed", "limit", "watch_id"}
	if got := slices.Sorted(maps.Keys(body.Error.Fields)); !slices.Equal(got, want) {
		t.Errorf("error fields = %v, want %v (%+v)", got, want, body.Error)
	}
}

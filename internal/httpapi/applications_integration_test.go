package httpapi

import (
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"testing"
)

type applicationPage struct {
	Applications []applicationJSON `json:"applications"`
	NextCursor   *string           `json:"next_cursor"`
}

func appPath(id int64) string { return "/api/v1/applications/" + strconv.FormatInt(id, 10) }

// mustCreateCompany creates a company and returns its id.
func mustCreateCompany(t *testing.T, h http.Handler, name string) int64 {
	t.Helper()
	var c companyJSON
	wantStatus(t, call(t, h, http.MethodPost, "/api/v1/companies", map[string]any{"name": name}, &c), http.StatusCreated)
	return c.ID
}

func mustCreateApplication(t *testing.T, h http.Handler, body map[string]any) applicationJSON {
	t.Helper()
	var a applicationJSON
	wantStatus(t, call(t, h, http.MethodPost, "/api/v1/applications", body, &a), http.StatusCreated)
	return a
}

func TestApplicationsCRUD(t *testing.T) {
	t.Parallel()
	h := newDBRouter(t)
	stripe := mustCreateCompany(t, h, "Stripe")
	fly := mustCreateCompany(t, h, "Fly.io")

	// Create with defaults: status "applied", company name joined in.
	var created applicationJSON
	rec := call(t, h, http.MethodPost, "/api/v1/applications", map[string]any{
		"company_id": stripe, "role": "Backend Engineer", "job_url": "https://stripe.com/jobs/1",
		"salary_min": 150000, "salary_max": 190000, "salary_currency": "usd",
	}, &created)
	wantStatus(t, rec, http.StatusCreated)
	if created.Status != "applied" || created.CompanyName != "Stripe" || *created.SalaryCurrency != "USD" {
		t.Fatalf("created = %+v", created)
	}
	if loc := rec.Header().Get("Location"); loc != appPath(created.ID) {
		t.Errorf("Location = %q", loc)
	}

	// Get
	var got applicationJSON
	wantStatus(t, call(t, h, http.MethodGet, appPath(created.ID), nil, &got), http.StatusOK)
	if got.Role != "Backend Engineer" || got.CompanyID != stripe {
		t.Errorf("got = %+v", got)
	}

	// Replace: move to another company, drop salary. Status is untouched by PUT.
	var updated applicationJSON
	wantStatus(t, call(t, h, http.MethodPut, appPath(created.ID), map[string]any{
		"company_id": fly, "role": "Platform Engineer",
	}, &updated), http.StatusOK)
	if updated.CompanyName != "Fly.io" || updated.SalaryMin != nil || updated.Status != "applied" {
		t.Errorf("updated = %+v", updated)
	}

	// Delete
	wantStatus(t, call(t, h, http.MethodDelete, appPath(created.ID), nil, nil), http.StatusNoContent)
	wantStatus(t, call(t, h, http.MethodGet, appPath(created.ID), nil, nil), http.StatusNotFound)
	wantStatus(t, call(t, h, http.MethodDelete, appPath(created.ID), nil, nil), http.StatusNotFound)
}

func TestApplicationsUnknownCompany(t *testing.T) {
	t.Parallel()
	h := newDBRouter(t)

	var errResp errorResponse
	rec := call(t, h, http.MethodPost, "/api/v1/applications", map[string]any{"company_id": 999, "role": "SWE"}, &errResp)
	wantStatus(t, rec, http.StatusUnprocessableEntity)
	if errResp.Error.Fields["company_id"] != "does not exist" {
		t.Errorf("fields = %v", errResp.Error.Fields)
	}

	co := mustCreateCompany(t, h, "Acme")
	app := mustCreateApplication(t, h, map[string]any{"company_id": co, "role": "SWE"})
	wantStatus(t, call(t, h, http.MethodPut, appPath(app.ID), map[string]any{"company_id": 999, "role": "SWE"}, nil), http.StatusUnprocessableEntity)
	wantStatus(t, call(t, h, http.MethodPut, appPath(12345), map[string]any{"company_id": co, "role": "SWE"}, nil), http.StatusNotFound)
}

func TestDeleteCompanyWithApplicationsIsRefused(t *testing.T) {
	t.Parallel()
	h := newDBRouter(t)
	co := mustCreateCompany(t, h, "Acme")
	app := mustCreateApplication(t, h, map[string]any{"company_id": co, "role": "SWE"})

	var errResp errorResponse
	wantStatus(t, call(t, h, http.MethodDelete, pathFor(co), nil, &errResp), http.StatusConflict)
	if errResp.Error.Code != codeConflict {
		t.Errorf("code = %q", errResp.Error.Code)
	}

	// Once the application is gone, the company can be deleted.
	wantStatus(t, call(t, h, http.MethodDelete, appPath(app.ID), nil, nil), http.StatusNoContent)
	wantStatus(t, call(t, h, http.MethodDelete, pathFor(co), nil, nil), http.StatusNoContent)
}

func TestApplicationsListFilters(t *testing.T) {
	t.Parallel()
	h := newDBRouter(t)
	a := mustCreateCompany(t, h, "Alpha")
	b := mustCreateCompany(t, h, "Beta")
	mustCreateApplication(t, h, map[string]any{"company_id": a, "role": "A1", "status": "wishlist"})
	mustCreateApplication(t, h, map[string]any{"company_id": a, "role": "A2"})
	mustCreateApplication(t, h, map[string]any{"company_id": b, "role": "B1"})

	roles := func(query string) []string {
		var page applicationPage
		wantStatus(t, call(t, h, http.MethodGet, "/api/v1/applications"+query, nil, &page), http.StatusOK)
		var out []string
		for _, app := range page.Applications {
			out = append(out, app.Role)
		}
		return out
	}

	tests := []struct {
		query string
		want  []string // newest first
	}{
		{"", []string{"B1", "A2", "A1"}},
		{"?status=applied", []string{"B1", "A2"}},
		{"?status=wishlist", []string{"A1"}},
		{"?company_id=" + strconv.FormatInt(a, 10), []string{"A2", "A1"}},
		{"?company_id=" + strconv.FormatInt(a, 10) + "&status=applied", []string{"A2"}},
		{"?status=offer", nil},
	}
	for _, tt := range tests {
		if got := roles(tt.query); !slices.Equal(got, tt.want) {
			t.Errorf("GET %s roles = %v, want %v", tt.query, got, tt.want)
		}
	}
}

func TestApplicationsPagination(t *testing.T) {
	t.Parallel()
	h := newDBRouter(t)
	co := mustCreateCompany(t, h, "Acme")

	const total = 7
	want := map[int64]bool{}
	for i := range total {
		app := mustCreateApplication(t, h, map[string]any{"company_id": co, "role": "Role " + strconv.Itoa(i)})
		want[app.ID] = true
	}

	// Walk every page of size 3: expect pages of 3, 3, 1 with no gaps or repeats.
	seen := map[int64]bool{}
	var sizes []int
	lastID := int64(1 << 62)
	path := "/api/v1/applications?limit=3"
	for range total { // bounded loop: a pagination bug can't hang the test
		var page applicationPage
		wantStatus(t, call(t, h, http.MethodGet, path, nil, &page), http.StatusOK)
		sizes = append(sizes, len(page.Applications))
		for _, app := range page.Applications {
			if seen[app.ID] {
				t.Fatalf("id %d returned twice", app.ID)
			}
			if app.ID >= lastID {
				t.Fatalf("ids not strictly descending: %d after %d", app.ID, lastID)
			}
			seen[app.ID], lastID = true, app.ID
		}
		if page.NextCursor == nil {
			break
		}
		path = "/api/v1/applications?limit=3&cursor=" + url.QueryEscape(*page.NextCursor)
	}

	if len(sizes) != 3 || sizes[0] != 3 || sizes[1] != 3 || sizes[2] != 1 {
		t.Errorf("page sizes = %v, want [3 3 1]", sizes)
	}
	if len(seen) != len(want) {
		t.Errorf("saw %d applications, want %d", len(seen), len(want))
	}

	// An exact multiple of the page size must not report a phantom next page.
	var page applicationPage
	wantStatus(t, call(t, h, http.MethodGet, "/api/v1/applications?limit=7", nil, &page), http.StatusOK)
	if len(page.Applications) != total || page.NextCursor != nil {
		t.Errorf("limit=%d: got %d rows, next_cursor=%v; want all rows and no cursor", total, len(page.Applications), page.NextCursor)
	}
}

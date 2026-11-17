package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"testing"

	"github.com/arjunshastry16/jobtrack/internal/store"
	"github.com/arjunshastry16/jobtrack/internal/testdb"
)

// newDBRouter returns a router backed by a fresh, migrated database for this test.
// Every response it produces is also checked against docs/openapi.yaml.
func newDBRouter(t *testing.T) http.Handler {
	t.Helper()
	pool := testdb.New(t)
	return contractChecked(t, NewRouter(Deps{
		Version: "test",
		DB:      pool,
		Store:   store.NewStore(pool),
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}))
}

// call sends a JSON request and decodes a JSON response into out (if non-nil).
func call(t *testing.T, h http.Handler, method, path string, body any, out any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequestWithContext(t.Context(), method, path, &buf)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if out != nil && rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatalf("%s %s: decode response %q: %v", method, path, rec.Body.String(), err)
		}
	}
	return rec
}

func wantStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, want, rec.Body.String())
	}
}

func TestCompaniesCRUD(t *testing.T) {
	t.Parallel()
	h := newDBRouter(t)

	// Create
	var created companyJSON
	rec := call(t, h, http.MethodPost, "/api/v1/companies", map[string]any{"name": " Stripe ", "website": "https://stripe.com"}, &created)
	wantStatus(t, rec, http.StatusCreated)
	if created.ID <= 0 || created.Name != "Stripe" || created.Website == nil || *created.Website != "https://stripe.com" {
		t.Fatalf("created = %+v", created)
	}
	if loc, want := rec.Header().Get("Location"), pathFor(created.ID); loc != want {
		t.Errorf("Location = %q, want %q", loc, want)
	}

	// Duplicate name, different case -> 409 (enforced by the lower(name) unique index)
	var errResp errorResponse
	rec = call(t, h, http.MethodPost, "/api/v1/companies", map[string]any{"name": "STRIPE"}, &errResp)
	wantStatus(t, rec, http.StatusConflict)
	if errResp.Error.Code != codeConflict {
		t.Errorf("code = %q, want %q", errResp.Error.Code, codeConflict)
	}

	// Get
	var got companyJSON
	rec = call(t, h, http.MethodGet, pathFor(created.ID), nil, &got)
	wantStatus(t, rec, http.StatusOK)
	if got.Name != "Stripe" || !got.CreatedAt.Equal(created.CreatedAt) {
		t.Errorf("got = %+v, want %+v", got, created)
	}

	// Update (PUT replaces: omitting website clears it)
	var updated companyJSON
	rec = call(t, h, http.MethodPut, pathFor(created.ID), map[string]any{"name": "Stripe Inc"}, &updated)
	wantStatus(t, rec, http.StatusOK)
	if updated.Name != "Stripe Inc" || updated.Website != nil {
		t.Errorf("updated = %+v", updated)
	}
	if !updated.UpdatedAt.After(created.UpdatedAt) {
		t.Errorf("updated_at did not advance: %v -> %v", created.UpdatedAt, updated.UpdatedAt)
	}

	// Delete, then it's gone
	rec = call(t, h, http.MethodDelete, pathFor(created.ID), nil, nil)
	wantStatus(t, rec, http.StatusNoContent)
	wantStatus(t, call(t, h, http.MethodGet, pathFor(created.ID), nil, nil), http.StatusNotFound)
	wantStatus(t, call(t, h, http.MethodDelete, pathFor(created.ID), nil, nil), http.StatusNotFound)
}

func TestCompaniesNotFound(t *testing.T) {
	t.Parallel()
	h := newDBRouter(t)

	var errResp errorResponse
	rec := call(t, h, http.MethodGet, "/api/v1/companies/999", nil, &errResp)
	wantStatus(t, rec, http.StatusNotFound)
	if errResp.Error.Code != codeNotFound {
		t.Errorf("code = %q, want %q", errResp.Error.Code, codeNotFound)
	}
	wantStatus(t, call(t, h, http.MethodPut, "/api/v1/companies/999", map[string]any{"name": "X"}, nil), http.StatusNotFound)
}

func TestCompaniesUpdateToDuplicateName(t *testing.T) {
	t.Parallel()
	h := newDBRouter(t)

	var a, b companyJSON
	wantStatus(t, call(t, h, http.MethodPost, "/api/v1/companies", map[string]any{"name": "Alpha"}, &a), http.StatusCreated)
	wantStatus(t, call(t, h, http.MethodPost, "/api/v1/companies", map[string]any{"name": "Beta"}, &b), http.StatusCreated)

	wantStatus(t, call(t, h, http.MethodPut, pathFor(b.ID), map[string]any{"name": "alpha"}, nil), http.StatusConflict)
}

func TestCompaniesListSortedAndEmpty(t *testing.T) {
	t.Parallel()
	h := newDBRouter(t)

	// Empty list is [] not null.
	rec := call(t, h, http.MethodGet, "/api/v1/companies", nil, nil)
	wantStatus(t, rec, http.StatusOK)
	if got := bytes.TrimSpace(rec.Body.Bytes()); string(got) != `{"companies":[]}` {
		t.Errorf("empty list body = %s", got)
	}

	for _, n := range []string{"zeta", "Alpha", "beta"} {
		wantStatus(t, call(t, h, http.MethodPost, "/api/v1/companies", map[string]any{"name": n}, nil), http.StatusCreated)
	}
	var list struct {
		Companies []companyJSON `json:"companies"`
	}
	wantStatus(t, call(t, h, http.MethodGet, "/api/v1/companies", nil, &list), http.StatusOK)
	var names []string
	for _, c := range list.Companies {
		names = append(names, c.Name)
	}
	if want := []string{"Alpha", "beta", "zeta"}; !slices.Equal(names, want) {
		t.Errorf("names = %v, want %v (case-insensitive order)", names, want)
	}
}

func pathFor(id int64) string {
	return "/api/v1/companies/" + strconv.FormatInt(id, 10)
}

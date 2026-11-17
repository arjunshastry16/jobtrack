package httpapi

import (
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestWatchInputNormalize(t *testing.T) {
	no := false
	tests := []struct {
		name         string
		in           watchInput
		wantErrs     []string
		wantKeywords []string
		wantEnabled  bool
	}{
		{
			name:         "cleans keywords, defaults enabled",
			in:           watchInput{CompanyID: 1, Board: " Greenhouse ", BoardToken: " stripe ", Keywords: []string{" Backend ", "backend", "", "Site  Reliability", "PLATFORM"}},
			wantKeywords: []string{"backend", "site reliability", "platform"},
			wantEnabled:  true,
		},
		{name: "no keywords means all", in: watchInput{CompanyID: 1, Board: "greenhouse", BoardToken: "x"}, wantKeywords: []string{}, wantEnabled: true},
		{name: "disabled", in: watchInput{CompanyID: 1, Board: "greenhouse", BoardToken: "x", Enabled: &no}, wantKeywords: []string{}, wantEnabled: false},
		{name: "lever", in: watchInput{CompanyID: 1, Board: "Lever", BoardToken: "meesho"}, wantKeywords: []string{}, wantEnabled: true},
		{name: "ashby", in: watchInput{CompanyID: 1, Board: "ashby", BoardToken: "sarvam"}, wantKeywords: []string{}, wantEnabled: true},
		{name: "missing everything", in: watchInput{}, wantErrs: []string{"company_id", "board", "board_token"}},
		{name: "unsupported board", in: watchInput{CompanyID: 1, Board: "workday", BoardToken: "x"}, wantErrs: []string{"board"}},
		{name: "token with slash", in: watchInput{CompanyID: 1, Board: "greenhouse", BoardToken: "a/b"}, wantErrs: []string{"board_token"}},
		{name: "keyword too long", in: watchInput{CompanyID: 1, Board: "greenhouse", BoardToken: "x", Keywords: []string{strings.Repeat("k", maxKeywordLen+1)}}, wantErrs: []string{"keywords"}},
		{name: "too many keywords", in: watchInput{CompanyID: 1, Board: "greenhouse", BoardToken: "x", Keywords: manyKeywords(maxKeywords + 1)}, wantErrs: []string{"keywords"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := tt.in
			kw, enabled, errs := in.normalize()
			if got := slices.Sorted(maps.Keys(errs)); !slices.Equal(got, slices.Sorted(slices.Values(tt.wantErrs))) {
				t.Fatalf("error fields = %v, want %v (%v)", got, tt.wantErrs, errs)
			}
			if len(tt.wantErrs) > 0 {
				return
			}
			if !slices.Equal(kw, tt.wantKeywords) || enabled != tt.wantEnabled {
				t.Errorf("keywords %q enabled %v; want %q %v", kw, enabled, tt.wantKeywords, tt.wantEnabled)
			}
		})
	}
}

func manyKeywords(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = "kw" + strconv.Itoa(i)
	}
	return out
}

func watchPath(id int64) string { return "/api/v1/watches/" + strconv.FormatInt(id, 10) }

func TestWatchesCRUD(t *testing.T) {
	t.Parallel()
	h := newDBRouter(t)
	co := mustCreateCompany(t, h, "Stripe")

	var created watchJSON
	rec := call(t, h, http.MethodPost, "/api/v1/watches", map[string]any{
		"company_id": co, "board": "greenhouse", "board_token": "stripe", "keywords": []string{"Backend", "SRE"},
	}, &created)
	wantStatus(t, rec, http.StatusCreated)
	if !created.Enabled || !slices.Equal(created.Keywords, []string{"backend", "sre"}) || created.LastCheckedAt != nil {
		t.Fatalf("created = %+v", created)
	}
	if rec.Header().Get("Location") != watchPath(created.ID) {
		t.Errorf("Location = %q", rec.Header().Get("Location"))
	}

	// One watch per board+token.
	wantStatus(t, call(t, h, http.MethodPost, "/api/v1/watches", map[string]any{
		"company_id": co, "board": "greenhouse", "board_token": "stripe",
	}, nil), http.StatusConflict)
	// Unknown company.
	wantStatus(t, call(t, h, http.MethodPost, "/api/v1/watches", map[string]any{
		"company_id": 9999, "board": "greenhouse", "board_token": "other",
	}, nil), http.StatusUnprocessableEntity)

	var list struct {
		Watches []watchJSON `json:"watches"`
	}
	wantStatus(t, call(t, h, http.MethodGet, "/api/v1/watches", nil, &list), http.StatusOK)
	if len(list.Watches) != 1 || list.Watches[0].ID != created.ID {
		t.Errorf("list = %+v", list.Watches)
	}

	// Replace: pause it and clear keywords.
	var updated watchJSON
	wantStatus(t, call(t, h, http.MethodPut, watchPath(created.ID), map[string]any{
		"company_id": co, "board": "greenhouse", "board_token": "stripe", "enabled": false,
	}, &updated), http.StatusOK)
	if updated.Enabled || len(updated.Keywords) != 0 {
		t.Errorf("updated = %+v", updated)
	}

	wantStatus(t, call(t, h, http.MethodDelete, watchPath(created.ID), nil, nil), http.StatusNoContent)
	wantStatus(t, call(t, h, http.MethodGet, watchPath(created.ID), nil, nil), http.StatusNotFound)
	wantStatus(t, call(t, h, http.MethodPut, watchPath(created.ID), map[string]any{
		"company_id": co, "board": "greenhouse", "board_token": "stripe",
	}, nil), http.StatusNotFound)
}

func TestDeletingCompanyDeletesItsWatches(t *testing.T) {
	t.Parallel()
	h := newDBRouter(t)
	co := mustCreateCompany(t, h, "Acme")
	var w watchJSON
	wantStatus(t, call(t, h, http.MethodPost, "/api/v1/watches", map[string]any{
		"company_id": co, "board": "greenhouse", "board_token": "acme",
	}, &w), http.StatusCreated)

	// Watches are configuration, not history: they don't block deleting the company.
	wantStatus(t, call(t, h, http.MethodDelete, pathFor(co), nil, nil), http.StatusNoContent)
	wantStatus(t, call(t, h, http.MethodGet, watchPath(w.ID), nil, nil), http.StatusNotFound)
}

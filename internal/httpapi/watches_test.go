package httpapi

import (
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/arjunshastry16/jobtrack/internal/boards"
)

// Sample URLs for Workday watches (constants, so gosec doesn't take a URL in a "Token"
// field for a credential).
const (
	nvidiaCareersURL = "https://nvidia.wd5.myworkdayjobs.com/en-US/NVIDIAExternalCareerSite?utm_source=x"
	notWorkdayURL    = "https://evil.example/External"
)

func TestWatchInputNormalize(t *testing.T) {
	no := false
	tests := []struct {
		name         string
		in           watchInput
		wantErrs     []string
		wantKeywords []string
		wantEnabled  bool
		wantStored   string // the board_token stored, checked when set
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
		{name: "smartrecruiters", in: watchInput{CompanyID: 1, Board: "SmartRecruiters", BoardToken: "Freshworks"}, wantKeywords: []string{}, wantEnabled: true},
		{name: "missing everything", in: watchInput{}, wantErrs: []string{"company_id", "board", "board_token"}},
		{
			name:         "workday URL is stored in canonical form",
			in:           watchInput{CompanyID: 1, Board: "workday", BoardToken: nvidiaCareersURL},
			wantKeywords: []string{}, wantEnabled: true, wantStored: "nvidia.wd5.myworkdayjobs.com/NVIDIAExternalCareerSite",
		},
		{name: "workday needs a careers URL", in: watchInput{CompanyID: 1, Board: "workday", BoardToken: "nvidia"}, wantErrs: []string{"board_token"}},
		{name: "workday host is pinned", in: watchInput{CompanyID: 1, Board: "workday", BoardToken: notWorkdayURL}, wantErrs: []string{"board_token"}},
		{name: "unsupported board", in: watchInput{CompanyID: 1, Board: "bamboohr", BoardToken: "x"}, wantErrs: []string{"board"}},
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
			if tt.wantStored != "" && in.BoardToken != tt.wantStored {
				t.Errorf("board_token = %q, want %q", in.BoardToken, tt.wantStored)
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

// Each supported board must be accepted by the database too (its CHECK constraints are
// separate from the Go validation, and the two have to be kept in step).
func TestEverySupportedBoardCanBeStored(t *testing.T) {
	t.Parallel()
	// A realistic token per board; for Workday, a URL that exercises every part of the
	// canonical form (filters, repeated filters, search text with a space).
	tokens := map[string]string{
		"greenhouse": "acme", "lever": "acme", "ashby": "acme", "smartrecruiters": "Acme-Corp",
		"workday": "https://acme.wd103.myworkdayjobs.com/en-US/Acme_Careers-1?q=site%20reliability%26sre" +
			"&locations=91336993fab910af6d6fb9748af4c1df&locations=f28ae3c49d2601aff203e114d301695f" +
			"&timeType=5509c0b5959810ac0029943377d47364&utm_source=linkedin",
	}
	h := newDBRouter(t)
	co := mustCreateCompany(t, h, "Acme")
	for _, b := range boards.Supported {
		tok, ok := tokens[b]
		if !ok {
			t.Errorf("no sample token for board %q", b)
			continue
		}
		var w watchJSON
		wantStatus(t, call(t, h, http.MethodPost, "/api/v1/watches", map[string]any{
			"company_id": co, "board": b, "board_token": tok,
		}, &w), http.StatusCreated)
		t.Logf("%s stored as %q", b, w.BoardToken)
	}
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

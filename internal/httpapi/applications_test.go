package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func i32(v int32) *int32 { return &v }

func TestApplicationFieldsNormalize(t *testing.T) {
	valid := func() applicationFields {
		return applicationFields{CompanyID: 1, Role: "Backend Engineer"}
	}
	tests := []struct {
		name     string
		mutate   func(*applicationFields)
		wantErrs []string
		check    func(t *testing.T, f applicationFields)
	}{
		{name: "minimal valid", mutate: func(*applicationFields) {}},
		{
			name: "trims and uppercases",
			mutate: func(f *applicationFields) {
				f.Role = "  SRE  "
				f.Source = ptr(" LinkedIn ")
				f.Location = ptr("   ")
				f.SalaryMin, f.SalaryMax, f.SalaryCurrency = i32(100), i32(200), ptr(" usd ")
			},
			check: func(t *testing.T, f applicationFields) {
				if f.Role != "SRE" || *f.Source != "LinkedIn" || f.Location != nil || *f.SalaryCurrency != "USD" {
					t.Errorf("normalized = role %q source %q location %v currency %q",
						f.Role, *f.Source, f.Location, *f.SalaryCurrency)
				}
			},
		},
		{name: "missing company and role", mutate: func(f *applicationFields) { f.CompanyID, f.Role = 0, " " }, wantErrs: []string{"company_id", "role"}},
		{name: "role too long", mutate: func(f *applicationFields) { f.Role = strings.Repeat("r", maxRoleLen+1) }, wantErrs: []string{"role"}},
		{name: "bad job url", mutate: func(f *applicationFields) { f.JobURL = ptr("example.com/job") }, wantErrs: []string{"job_url"}},
		{name: "source too long", mutate: func(f *applicationFields) { f.Source = ptr(strings.Repeat("s", maxSourceLen+1)) }, wantErrs: []string{"source"}},
		{name: "salary without currency", mutate: func(f *applicationFields) { f.SalaryMin = i32(1) }, wantErrs: []string{"salary_currency"}},
		{name: "bad currency", mutate: func(f *applicationFields) { f.SalaryMax, f.SalaryCurrency = i32(1), ptr("dollars") }, wantErrs: []string{"salary_currency"}},
		{name: "negative salary", mutate: func(f *applicationFields) { f.SalaryMin, f.SalaryCurrency = i32(-1), ptr("EUR") }, wantErrs: []string{"salary_min"}},
		{name: "min above max", mutate: func(f *applicationFields) {
			f.SalaryMin, f.SalaryMax, f.SalaryCurrency = i32(300), i32(200), ptr("EUR")
		}, wantErrs: []string{"salary_max"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := valid()
			tt.mutate(&f)
			errs := map[string]string{}
			f.normalize(errs)

			got := slices.Sorted(maps.Keys(errs))
			want := slices.Sorted(slices.Values(tt.wantErrs))
			if !slices.Equal(got, want) {
				t.Fatalf("error fields = %v, want %v (%v)", got, want, errs)
			}
			if tt.check != nil {
				tt.check(t, f)
			}
		})
	}
}

func TestCursorRoundTrip(t *testing.T) {
	for _, id := range []int64{1, 42, 9_007_199_254_740_993} {
		got, err := decodeCursor(encodeCursor(id))
		if err != nil || got != id {
			t.Errorf("decodeCursor(encodeCursor(%d)) = %d, %v", id, got, err)
		}
	}
	for _, bad := range []string{"", "!!!", encodeCursorRaw("v2:5"), encodeCursorRaw("v1:abc"), encodeCursorRaw("v1:0"), encodeCursorRaw("v1:-3")} {
		if _, err := decodeCursor(bad); err == nil {
			t.Errorf("decodeCursor(%q) succeeded, want error", bad)
		}
	}
}

// encodeCursorRaw builds a cursor from arbitrary content, to test rejection of tampered ones.
func encodeCursorRaw(s string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(s))
}

func TestParsePageSize(t *testing.T) {
	tests := map[string]int{"": defaultPageSize, "1": 1, "200": 200}
	for in, want := range tests {
		if got, err := parsePageSize(in); err != nil || got != want {
			t.Errorf("parsePageSize(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"0", "201", "-1", "ten", "1.5"} {
		if _, err := parsePageSize(bad); err == nil {
			t.Errorf("parsePageSize(%q) succeeded, want error", bad)
		}
	}
}

// Requests rejected before the store is used, so no database is needed.
func TestApplicationRequestRejections(t *testing.T) {
	h := newTestRouter(fakeDB{})
	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		wantStatus int
		wantCode   string
		wantField  string
	}{
		{name: "bad limit", method: http.MethodGet, path: "/api/v1/applications?limit=0", wantStatus: 400, wantCode: codeInvalidQuery, wantField: "limit"},
		{name: "bad status filter", method: http.MethodGet, path: "/api/v1/applications?status=hired", wantStatus: 400, wantCode: codeInvalidQuery, wantField: "status"},
		{name: "bad company filter", method: http.MethodGet, path: "/api/v1/applications?company_id=x", wantStatus: 400, wantCode: codeInvalidQuery, wantField: "company_id"},
		{name: "bad cursor", method: http.MethodGet, path: "/api/v1/applications?cursor=nope", wantStatus: 400, wantCode: codeInvalidQuery, wantField: "cursor"},
		{name: "bad initial status", method: http.MethodPost, path: "/api/v1/applications", body: `{"company_id":1,"role":"SWE","status":"hired"}`, wantStatus: 422, wantCode: codeValidationFailed, wantField: "status"},
		{name: "status not editable via PUT", method: http.MethodPut, path: "/api/v1/applications/1", body: `{"company_id":1,"role":"SWE","status":"offer"}`, wantStatus: 400, wantCode: codeInvalidJSON},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), tt.method, tt.path, strings.NewReader(tt.body))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body: %s)", rec.Code, tt.wantStatus, rec.Body)
			}
			var got errorResponse
			if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
				t.Fatal(err)
			}
			if got.Error.Code != tt.wantCode {
				t.Errorf("code = %q, want %q", got.Error.Code, tt.wantCode)
			}
			if tt.wantField != "" {
				if _, ok := got.Error.Fields[tt.wantField]; !ok {
					t.Errorf("fields = %v, want an error for %q", got.Error.Fields, tt.wantField)
				}
			}
		})
	}
}

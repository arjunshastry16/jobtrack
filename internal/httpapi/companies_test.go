package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func ptr(s string) *string { return &s }

func TestCompanyInputNormalize(t *testing.T) {
	tests := []struct {
		name        string
		in          companyInput
		wantErrs    []string // fields expected to be invalid
		wantName    string
		wantWebsite *string
	}{
		{name: "valid, trims", in: companyInput{Name: "  Stripe ", Website: ptr(" https://stripe.com ")}, wantName: "Stripe", wantWebsite: ptr("https://stripe.com")},
		{name: "no website", in: companyInput{Name: "Acme"}, wantName: "Acme"},
		{name: "blank website becomes nil", in: companyInput{Name: "Acme", Website: ptr("  ")}, wantName: "Acme"},
		{name: "missing name", in: companyInput{Name: "   "}, wantErrs: []string{"name"}},
		{name: "name too long", in: companyInput{Name: strings.Repeat("a", maxCompanyNameLen+1)}, wantErrs: []string{"name"}},
		{name: "name at limit (multi-byte)", in: companyInput{Name: strings.Repeat("é", maxCompanyNameLen)}, wantName: strings.Repeat("é", maxCompanyNameLen)},
		{name: "website not a URL", in: companyInput{Name: "Acme", Website: ptr("acme dot com")}, wantErrs: []string{"website"}},
		{name: "website wrong scheme", in: companyInput{Name: "Acme", Website: ptr("javascript:alert(1)")}, wantErrs: []string{"website"}},
		{name: "both invalid", in: companyInput{Name: "", Website: ptr("ftp://x")}, wantErrs: []string{"name", "website"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := tt.in
			errs := in.normalize()
			if len(errs) != len(tt.wantErrs) {
				t.Fatalf("errors = %v, want fields %v", errs, tt.wantErrs)
			}
			for _, f := range tt.wantErrs {
				if _, ok := errs[f]; !ok {
					t.Errorf("missing error for %q in %v", f, errs)
				}
			}
			if len(tt.wantErrs) > 0 {
				return
			}
			if in.Name != tt.wantName {
				t.Errorf("Name = %q, want %q", in.Name, tt.wantName)
			}
			if (in.Website == nil) != (tt.wantWebsite == nil) || (in.Website != nil && *in.Website != *tt.wantWebsite) {
				t.Errorf("Website = %v, want %v", deref(in.Website), deref(tt.wantWebsite))
			}
		})
	}
}

func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

// These requests are rejected before the store is touched, so the router needs no database.
func TestCompanyRequestRejections(t *testing.T) {
	h := newTestRouter(fakeDB{})
	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		wantStatus int
		wantCode   string
	}{
		{name: "empty body", method: http.MethodPost, path: "/api/v1/companies", body: "", wantStatus: 400, wantCode: codeInvalidJSON},
		{name: "malformed JSON", method: http.MethodPost, path: "/api/v1/companies", body: `{"name":`, wantStatus: 400, wantCode: codeInvalidJSON},
		{name: "unknown field", method: http.MethodPost, path: "/api/v1/companies", body: `{"title":"Acme"}`, wantStatus: 400, wantCode: codeInvalidJSON},
		{name: "two objects", method: http.MethodPost, path: "/api/v1/companies", body: `{"name":"a"}{"name":"b"}`, wantStatus: 400, wantCode: codeInvalidJSON},
		{name: "too large", method: http.MethodPost, path: "/api/v1/companies", body: `{"name":"` + strings.Repeat("a", maxBodyBytes) + `"}`, wantStatus: 413, wantCode: codeBodyTooLarge},
		{name: "validation", method: http.MethodPost, path: "/api/v1/companies", body: `{"name":""}`, wantStatus: 422, wantCode: codeValidationFailed},
		{name: "non-numeric id", method: http.MethodGet, path: "/api/v1/companies/abc", wantStatus: 400, wantCode: codeInvalidID},
		{name: "zero id", method: http.MethodDelete, path: "/api/v1/companies/0", wantStatus: 400, wantCode: codeInvalidID},
		{name: "negative id on update", method: http.MethodPut, path: "/api/v1/companies/-1", body: `{"name":"a"}`, wantStatus: 400, wantCode: codeInvalidID},
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
				t.Fatalf("decode error body: %v", err)
			}
			if got.Error.Code != tt.wantCode {
				t.Errorf("error code = %q, want %q", got.Error.Code, tt.wantCode)
			}
		})
	}
}

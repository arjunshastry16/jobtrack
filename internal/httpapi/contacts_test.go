package httpapi

import (
	"maps"
	"slices"
	"strings"
	"testing"
)

func TestContactInputNormalize(t *testing.T) {
	tests := []struct {
		name     string
		in       contactInput
		wantErrs []string
	}{
		{name: "minimal", in: contactInput{CompanyID: 1, Name: "Ada"}},
		{name: "full", in: contactInput{CompanyID: 1, Name: " Ada ", Title: ptr("Recruiter"),
			Email: ptr(" ada@example.com "), LinkedinURL: ptr("https://www.linkedin.com/in/ada")}},
		{name: "bare linkedin host", in: contactInput{CompanyID: 1, Name: "Ada", LinkedinURL: ptr("https://linkedin.com/in/ada")}},
		{name: "blank optionals become nil", in: contactInput{CompanyID: 1, Name: "Ada", Email: ptr(" "), Title: ptr("")}},
		{name: "missing company and name", in: contactInput{}, wantErrs: []string{"company_id", "name"}},
		{name: "display-name email", in: contactInput{CompanyID: 1, Name: "Ada", Email: ptr("Ada <ada@example.com>")}, wantErrs: []string{"email"}},
		{name: "not an email", in: contactInput{CompanyID: 1, Name: "Ada", Email: ptr("ada-at-example")}, wantErrs: []string{"email"}},
		{name: "email without dot in domain", in: contactInput{CompanyID: 1, Name: "Ada", Email: ptr("ada@localhost")}, wantErrs: []string{"email"}},
		{name: "non-linkedin url", in: contactInput{CompanyID: 1, Name: "Ada", LinkedinURL: ptr("https://example.com/in/ada")}, wantErrs: []string{"linkedin_url"}},
		{name: "lookalike host", in: contactInput{CompanyID: 1, Name: "Ada", LinkedinURL: ptr("https://evil-linkedin.com/in/ada")}, wantErrs: []string{"linkedin_url"}},
		{name: "title too long", in: contactInput{CompanyID: 1, Name: "Ada", Title: ptr(strings.Repeat("t", maxTitleLen+1))}, wantErrs: []string{"title"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := tt.in
			errs := in.normalize()
			got := slices.Sorted(maps.Keys(errs))
			if want := slices.Sorted(slices.Values(tt.wantErrs)); !slices.Equal(got, want) {
				t.Fatalf("error fields = %v, want %v (%v)", got, want, errs)
			}
			if len(tt.wantErrs) == 0 && (in.Name != strings.TrimSpace(tt.in.Name)) {
				t.Errorf("name not trimmed: %q", in.Name)
			}
		})
	}
}

func TestNoteInputNormalize(t *testing.T) {
	ok := noteInput{Body: "  prep: read their engineering blog  "}
	if errs := ok.normalize(); errs != nil || ok.Body != "prep: read their engineering blog" {
		t.Errorf("valid note: errs %v, body %q", errs, ok.Body)
	}
	for _, body := range []string{"", "   ", strings.Repeat("x", maxNoteBodyLen+1)} {
		in := noteInput{Body: body}
		if in.normalize() == nil {
			t.Errorf("note body of length %d accepted, want error", len(body))
		}
	}
}

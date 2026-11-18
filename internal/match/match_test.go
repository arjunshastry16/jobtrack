package match_test

import (
	"testing"

	"github.com/arjunshastry16/jobtrack/internal/match"
)

func TestMatches(t *testing.T) {
	sre := []string{"backend", "site reliability", "SRE", "c++"}
	india := []string{"india", "remote"}
	tests := []struct {
		name                string
		keywords, locations []string
		title, location     string
		want                bool
	}{
		{"no filters", nil, nil, "Anything", "Anywhere", true},

		// Keywords: whole words in the title.
		{"keyword", sre, nil, "Senior Backend Engineer", "", true},
		{"keyword is case-insensitive", sre, nil, "Lead SRE", "", true},
		{"phrase needs consecutive words", sre, nil, "Site Reliability Engineer", "", true},
		{"phrase words apart", sre, nil, "Reliability Site Lead", "", false},
		{"no partial words", []string{"ai"}, nil, "Maintenance Technician", "", false},
		{"short keyword as a word", []string{"ai"}, nil, "AI/ML Engineer", "", true},
		{"punctuation splits words", sre, nil, "Engineer (Backend)", "", true},
		{"c++ is a word", sre, nil, "C++ Developer", "", true},
		{"c is not c++", sre, nil, "C Developer", "", false},
		{"hyphenated is two words", sre, nil, "Back-end Developer", "", false},
		{"no keyword matches", sre, nil, "Account Executive", "", false},

		// Locations: whole words in the location text.
		{"location", nil, india, "x", "Bengaluru, Karnataka, India", true},
		{"no partial location", nil, india, "x", "Indianapolis, Indiana", false},
		{"any listed location", nil, india, "x", "Remote - US", true},
		{"several places", nil, india, "x", "London, United Kingdom; India", true},
		{"wrong place", nil, india, "x", "Austin, TX; United States", false},
		{"unknown location kept", nil, india, "x", "", true},
		{"location count kept", nil, india, "x", "2 Locations", true},
		{"one location kept", nil, india, "x", "1 Location", true},
		{"city alias", nil, []string{"bangalore"}, "x", "Bengaluru, India", true},
		{"alias the other way", nil, []string{"gurugram"}, "x", "Gurgaon, Haryana", true},

		// Both filters must pass.
		{"both match", sre, india, "Backend Engineer", "Pune, India", true},
		{"title fails", sre, india, "Sales Lead", "Pune, India", false},
		{"location fails", sre, india, "Backend Engineer", "Berlin, Germany", false},

		// Terms with no words are ignored rather than matching everything or nothing.
		{"empty terms ignored", []string{"", "--"}, []string{" "}, "Anything", "Anywhere", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := match.New(tt.keywords, tt.locations).Matches(tt.title, tt.location); got != tt.want {
				t.Errorf("Matches(%q, %q) with keywords %q, locations %q = %v, want %v",
					tt.title, tt.location, tt.keywords, tt.locations, got, tt.want)
			}
		})
	}
}

func TestNormalize(t *testing.T) {
	tests := map[string]string{
		"  Site   Reliability ": "site reliability",
		"Back-End":              "back end",
		"C++":                   "c++",
		"Bangalore":             "bengaluru",
		"--":                    "",
		"":                      "",
	}
	for in, want := range tests {
		if got := match.Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

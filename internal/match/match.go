// Package match decides whether a job posting passes a watch's filters.
//
// Filters match whole words, ignoring case: "india" matches "Bengaluru, India" but not
// "Indianapolis, Indiana", and "ai" matches "AI Engineer" but not "Maintenance". A term
// of several words ("site reliability") must appear as consecutive words. Words are runs
// of letters, digits, '+' and '#', so "C++" and "C#" work; "Back-end" is two words, so
// list "backend" and "back end" to catch both spellings.
package match

import (
	"regexp"
	"slices"
	"strings"
	"unicode"
)

// Normalize returns the canonical form of a filter term, the form watches store: its
// words, lower-case, separated by single spaces. It is "" if s has no words.
func Normalize(s string) string { return strings.Join(words(s), " ") }

// Filter is a watch's compiled filters.
type Filter struct {
	keywords  [][]string // each term split into words; empty means any title
	locations [][]string // likewise; empty means anywhere
}

// New compiles a watch's keywords (matched against posting titles) and locations
// (matched against posting locations). A posting must match at least one keyword, and
// at least one location, unless that list is empty.
func New(keywords, locations []string) Filter {
	return Filter{keywords: compile(keywords), locations: compile(locations)}
}

// Matches reports whether a posting with this title and location passes the filter.
//
// A posting whose location is unknown passes any location filter: hiding it would be
// worse than showing it. Unknown means empty, or only a count such as Workday's
// "2 Locations" (its watches are usually filtered by country in the careers-site URL
// already).
func (f Filter) Matches(title, location string) bool {
	if !matchesAny(f.keywords, words(title)) {
		return false
	}
	if len(f.locations) == 0 {
		return true
	}
	loc := words(location)
	return unknownLocation(loc) || matchesAny(f.locations, loc)
}

func compile(terms []string) [][]string {
	out := make([][]string, 0, len(terms))
	for _, t := range terms {
		if w := words(t); len(w) > 0 {
			out = append(out, w)
		}
	}
	return out
}

// matchesAny reports whether any term appears as consecutive words of text. No terms
// means no restriction.
func matchesAny(terms [][]string, text []string) bool {
	if len(terms) == 0 {
		return true
	}
	for _, term := range terms {
		for i := 0; i+len(term) <= len(text); i++ {
			if slices.Equal(text[i:i+len(term)], term) {
				return true
			}
		}
	}
	return false
}

var locationCount = regexp.MustCompile(`^[0-9]+ locations?$`)

func unknownLocation(loc []string) bool {
	return len(loc) == 0 || locationCount.MatchString(strings.Join(loc, " "))
}

// aliases maps old or alternative city names to the one boards mostly use, so a filter
// for "bangalore" finds "Bengaluru" and the other way round.
var aliases = map[string]string{
	"bangalore": "bengaluru",
	"gurgaon":   "gurugram",
	"bombay":    "mumbai",
	"madras":    "chennai",
	"calcutta":  "kolkata",
}

// words splits s into lower-case words, with aliases applied.
func words(s string) []string {
	ws := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '+' && r != '#'
	})
	for i, w := range ws {
		if a, ok := aliases[w]; ok {
			ws[i] = a
		}
	}
	return ws
}

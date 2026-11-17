package boards

import "strings"

// countryNames maps ISO 3166-1 alpha-2 codes (as some boards report them) to names, so a
// location filter like "india" matches a posting whose only country hint is "IN". It
// covers the countries that matter most for this tracker; unknown codes are kept as-is.
var countryNames = map[string]string{
	"IN": "India", "US": "United States", "GB": "United Kingdom", "CA": "Canada",
	"SG": "Singapore", "AE": "United Arab Emirates", "DE": "Germany", "NL": "Netherlands",
	"IE": "Ireland", "FR": "France", "AU": "Australia", "JP": "Japan", "PL": "Poland",
	"ES": "Spain", "BR": "Brazil", "MX": "Mexico", "IL": "Israel", "CH": "Switzerland",
}

// countryName returns the country name for an ISO alpha-2 code, or the code itself.
func countryName(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	if name, ok := countryNames[code]; ok {
		return name
	}
	return code
}

// joinLocations combines a posting's location parts into one string for display and
// filtering: trimmed, de-duplicated (case-insensitively), in order, "; "-separated.
func joinLocations(parts ...string) string {
	seen := map[string]bool{}
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.Join(strings.Fields(p), " ")
		key := strings.ToLower(p)
		if p == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, p)
	}
	return strings.Join(out, "; ")
}

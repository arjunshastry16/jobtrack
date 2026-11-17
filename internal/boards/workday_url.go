package boards

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

// WorkdaySite is one company's Workday careers site, plus any filters the user applied
// on that site before copying its URL (e.g. location = India).
type WorkdaySite struct {
	Host   string     // e.g. "nvidia.wd5.myworkdayjobs.com"
	Tenant string     // the first label of Host, e.g. "nvidia"
	Site   string     // e.g. "NVIDIAExternalCareerSite"
	Search string     // the careers page's search box (?q=), may be empty
	Facets url.Values // filters, facet name -> Workday ids, e.g. locationCountry -> [c4f7…]
}

var (
	wdHostPattern   = regexp.MustCompile(`^([a-z0-9][a-z0-9-]{0,62})\.wd[0-9]{1,3}\.myworkdayjobs\.com$`)
	wdSitePattern   = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}$`)
	wdLocalePattern = regexp.MustCompile(`^[a-z]{2}(-[A-Za-z]{2})?$`) // "en-US" in /en-US/Site
	wdFacetName     = regexp.MustCompile(`^[A-Za-z0-9_]{1,50}$`)
	wdFacetID       = regexp.MustCompile(`^[0-9a-f]{32}$`) // Workday ids are 32 hex digits
)

const (
	wdMaxURLLen    = 2000
	wdMaxSearchLen = 100
	wdMaxFacetIDs  = 20
)

// ParseWorkdayURL accepts a careers-site URL as copied from the browser, for example
//
//	https://nvidia.wd5.myworkdayjobs.com/en-US/NVIDIAExternalCareerSite?locationHierarchy1=2fcb…
//
// or the canonical token Token returns. The locale segment, job paths and tracking
// parameters (utm_*, source, …) are dropped; the search text and filters are kept, so a
// watch fetches only what the user filtered the page down to. Only *.myworkdayjobs.com
// hosts are accepted: companies on a custom domain aren't supported.
func ParseWorkdayURL(raw string) (WorkdaySite, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return WorkdaySite{}, errors.New("empty Workday URL")
	}
	if len(raw) > wdMaxURLLen {
		return WorkdaySite{}, errors.New("URL is too long")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return WorkdaySite{}, errors.New("not a valid URL")
	}
	if (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.Port() != "" {
		return WorkdaySite{}, errors.New("must be a plain https:// careers-site URL")
	}
	host := strings.ToLower(u.Hostname())
	m := wdHostPattern.FindStringSubmatch(host)
	if m == nil {
		return WorkdaySite{}, errors.New("host must be <company>.wd<N>.myworkdayjobs.com")
	}

	var segs []string
	for _, s := range strings.Split(u.Path, "/") {
		if s != "" {
			segs = append(segs, s)
		}
	}
	if len(segs) > 0 && wdLocalePattern.MatchString(segs[0]) {
		segs = segs[1:]
	}
	if len(segs) == 0 || !wdSitePattern.MatchString(segs[0]) {
		return WorkdaySite{}, errors.New("URL must include the careers site name, e.g. /External")
	}

	site := WorkdaySite{Host: host, Tenant: m[1], Site: segs[0], Facets: url.Values{}}
	ids := 0
	for name, vals := range u.Query() {
		if name == "q" {
			site.Search = strings.Join(strings.Fields(strings.Join(vals, " ")), " ")
			if utf8.RuneCountInString(site.Search) > wdMaxSearchLen {
				return WorkdaySite{}, errors.New("search text is too long")
			}
			continue
		}
		// Filters have Workday ids as values; anything else (utm_source=…) is not a filter.
		if !wdFacetName.MatchString(name) || !allMatch(wdFacetID, vals) {
			continue
		}
		site.Facets[name] = vals
		if ids += len(vals); ids > wdMaxFacetIDs {
			return WorkdaySite{}, errors.New("too many filters")
		}
	}
	return site, nil
}

// Token is the canonical form stored in watches.board_token:
// host/site, then the search and filters as a sorted query string.
func (s WorkdaySite) Token() string {
	q := url.Values{}
	for name, vals := range s.Facets {
		q[name] = vals
	}
	if s.Search != "" {
		q.Set("q", s.Search)
	}
	tok := s.Host + "/" + s.Site
	if len(q) > 0 {
		tok += "?" + q.Encode()
	}
	return tok
}

func allMatch(re *regexp.Regexp, vals []string) bool {
	for _, v := range vals {
		if !re.MatchString(v) {
			return false
		}
	}
	return len(vals) > 0
}

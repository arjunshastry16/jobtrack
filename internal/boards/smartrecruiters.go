package boards

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// SmartRecruiters reads SmartRecruiters' public Posting API:
//
//	GET https://api.smartrecruiters.com/v1/companies/{company}/postings?offset=N&limit=100
//
// Note: for an unknown company the API answers 200 with no postings rather than 404, so
// a mistyped token looks like a company with no openings.
type SmartRecruiters struct {
	baseURL  string
	client   *http.Client
	pageSize int
}

const (
	srPageSize = 100 // the API's maximum
	// srMaxPages allows 10,000 postings (Bosch alone lists ~5,000 worldwide).
	srMaxPages = 100
)

// NewSmartRecruiters returns a SmartRecruiters board client. A nil client gets a default
// with a 30s timeout.
func NewSmartRecruiters(client *http.Client) *SmartRecruiters {
	return NewSmartRecruitersAt("https://api.smartrecruiters.com", client)
}

// NewSmartRecruitersAt is NewSmartRecruiters with a different API base URL (for tests).
func NewSmartRecruitersAt(baseURL string, client *http.Client) *SmartRecruiters {
	return &SmartRecruiters{baseURL: baseURL, client: defaultClient(client), pageSize: srPageSize}
}

// Name implements Board.
func (s *SmartRecruiters) Name() string { return "smartrecruiters" }

type srResponse struct {
	TotalFound int `json:"totalFound"`
	Content    []struct {
		ID           string `json:"id"`
		Name         string `json:"name"` // the job title
		ReleasedDate string `json:"releasedDate"`
		Visibility   string `json:"visibility"`
		Location     struct {
			City         string `json:"city"`
			Country      string `json:"country"` // ISO alpha-2, lower case: "in"
			Remote       bool   `json:"remote"`
			FullLocation string `json:"fullLocation"` // e.g. "London, , United Kingdom"
		} `json:"location"`
	} `json:"content"`
}

// Fetch implements Board.
func (s *SmartRecruiters) Fetch(ctx context.Context, token string) ([]Posting, error) {
	if !ValidToken(token) {
		return nil, fmt.Errorf("boards: smartrecruiters: invalid company token %q", token)
	}
	var out []Posting
	seen := map[string]bool{}
	for page := range srMaxPages {
		resp, err := s.fetchPage(ctx, token, page*s.pageSize)
		if err != nil {
			return nil, err
		}
		for _, p := range resp.Content {
			if p.ID == "" || strings.TrimSpace(p.Name) == "" || seen[p.ID] {
				continue
			}
			if strings.EqualFold(p.Visibility, "INTERNAL") {
				continue // internal postings aren't meant to be public
			}
			seen[p.ID] = true
			out = append(out, Posting{
				ExternalID:  p.ID,
				Title:       strings.TrimSpace(p.Name),
				Location:    srLocation(p.Location.FullLocation, p.Location.City, p.Location.Country, p.Location.Remote),
				URL:         "https://jobs.smartrecruiters.com/" + url.PathEscape(token) + "/" + url.PathEscape(p.ID),
				PublishedAt: parseTime(p.ReleasedDate),
			})
		}
		// Stop on a short page, or once the reported total is reached.
		if len(resp.Content) < s.pageSize || (page+1)*s.pageSize >= resp.TotalFound {
			return out, nil
		}
	}
	return out, fmt.Errorf("boards: smartrecruiters: %s has more than %d postings; stopped paging", token, srMaxPages*s.pageSize)
}

func (s *SmartRecruiters) fetchPage(ctx context.Context, token string, offset int) (srResponse, error) {
	q := url.Values{"offset": {strconv.Itoa(offset)}, "limit": {strconv.Itoa(s.pageSize)}}
	body, err := getJSON(ctx, s.client, s.Name(),
		s.baseURL+"/v1/companies/"+url.PathEscape(token)+"/postings?"+q.Encode())
	if err != nil {
		return srResponse{}, err
	}
	defer func() { _ = body.Close() }()
	var resp srResponse
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		return srResponse{}, fmt.Errorf("boards: smartrecruiters: decode %s: %w", token, err)
	}
	return resp, nil
}

// srLocation builds clean location text. fullLocation can contain empty parts
// ("London, , United Kingdom"); the country name is added only if it isn't already there.
func srLocation(fullLocation, city, countryCode string, remote bool) string {
	var parts []string
	for _, p := range strings.Split(fullLocation, ",") {
		if p = strings.TrimSpace(p); p != "" {
			parts = append(parts, p)
		}
	}
	loc := strings.Join(parts, ", ")
	if loc == "" {
		loc = city
	}
	extra := []string{loc}
	if name := countryName(countryCode); name != "" && !strings.Contains(strings.ToLower(loc), strings.ToLower(name)) {
		extra = append(extra, name)
	}
	if remote {
		extra = append(extra, "Remote")
	}
	return joinLocations(extra...)
}

package boards

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Lever reads Lever's public postings API:
//
//	GET https://api.lever.co/v0/postings/{company}?mode=json&skip=N&limit=M
//
// Results are fetched in pages so one huge board can't produce one huge response.
// (Companies on Lever's EU instance use api.eu.lever.co and aren't supported yet.)
type Lever struct {
	baseURL  string
	client   *http.Client
	pageSize int
}

const (
	leverPageSize = 100
	// leverMaxPages bounds the loop if the API ever stops honouring skip.
	leverMaxPages = 50
)

// NewLever returns a Lever board client. A nil client gets a default with a 30s timeout.
func NewLever(client *http.Client) *Lever {
	return NewLeverAt("https://api.lever.co", client)
}

// NewLeverAt is NewLever with a different API base URL (tests use a local server).
func NewLeverAt(baseURL string, client *http.Client) *Lever {
	return &Lever{baseURL: baseURL, client: defaultClient(client), pageSize: leverPageSize}
}

// Name implements Board.
func (l *Lever) Name() string { return "lever" }

type leverPosting struct {
	ID         string `json:"id"`
	Text       string `json:"text"` // the job title
	HostedURL  string `json:"hostedUrl"`
	CreatedAt  int64  `json:"createdAt"` // Unix milliseconds
	Country    string `json:"country"`   // ISO alpha-2, e.g. "IN"
	Categories struct {
		Location     string   `json:"location"`
		AllLocations []string `json:"allLocations"`
	} `json:"categories"`
}

// Fetch implements Board.
func (l *Lever) Fetch(ctx context.Context, token string) ([]Posting, error) {
	if !ValidToken(token) {
		return nil, fmt.Errorf("boards: lever: invalid company token %q", token)
	}
	var out []Posting
	seen := map[string]bool{}
	for page := range leverMaxPages {
		q := url.Values{
			"mode":  {"json"},
			"skip":  {strconv.Itoa(page * l.pageSize)},
			"limit": {strconv.Itoa(l.pageSize)},
		}
		batch, err := l.fetchPage(ctx, token, q)
		if err != nil {
			return nil, err
		}
		for _, p := range batch {
			if p.ID == "" || strings.TrimSpace(p.Text) == "" || seen[p.ID] {
				continue // malformed, or repeated across pages
			}
			seen[p.ID] = true
			// Lever's location text often omits the country ("Bangalore, Karnataka");
			// add its name so a location filter like "india" still matches.
			locs := append([]string{p.Categories.Location}, p.Categories.AllLocations...)
			locs = append(locs, countryName(p.Country))
			out = append(out, Posting{
				ExternalID:  p.ID,
				Title:       strings.TrimSpace(p.Text),
				Location:    joinLocations(locs...),
				URL:         p.HostedURL,
				PublishedAt: unixMillis(p.CreatedAt),
			})
		}
		if len(batch) < l.pageSize {
			return out, nil // last page
		}
	}
	return out, fmt.Errorf("boards: lever: %s has more than %d postings; stopped paging", token, leverMaxPages*l.pageSize)
}

func (l *Lever) fetchPage(ctx context.Context, token string, q url.Values) ([]leverPosting, error) {
	body, err := getJSON(ctx, l.client, l.Name(), l.baseURL+"/v0/postings/"+url.PathEscape(token)+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	defer func() { _ = body.Close() }()
	var batch []leverPosting
	if err := json.NewDecoder(body).Decode(&batch); err != nil {
		return nil, fmt.Errorf("boards: lever: decode %s: %w", token, err)
	}
	return batch, nil
}

func unixMillis(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}

package boards

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Ashby reads Ashby's public job-posting API:
//
//	GET https://api.ashbyhq.com/posting-api/job-board/{organization}
type Ashby struct {
	baseURL string
	client  *http.Client
}

// NewAshby returns an Ashby board client. A nil client gets a default with a 30s timeout.
func NewAshby(client *http.Client) *Ashby {
	return NewAshbyAt("https://api.ashbyhq.com", client)
}

// NewAshbyAt is NewAshby with a different API base URL (tests use a local server).
func NewAshbyAt(baseURL string, client *http.Client) *Ashby {
	return &Ashby{baseURL: baseURL, client: defaultClient(client)}
}

// Name implements Board.
func (a *Ashby) Name() string { return "ashby" }

type ashbyResponse struct {
	Jobs []struct {
		ID                 string `json:"id"`
		Title              string `json:"title"`
		JobURL             string `json:"jobUrl"`
		PublishedAt        string `json:"publishedAt"`
		IsListed           *bool  `json:"isListed"`
		IsRemote           bool   `json:"isRemote"`
		Location           string `json:"location"`
		SecondaryLocations []struct {
			Location string `json:"location"`
		} `json:"secondaryLocations"`
		Address struct {
			PostalAddress struct {
				AddressLocality string `json:"addressLocality"`
				AddressCountry  string `json:"addressCountry"`
			} `json:"postalAddress"`
		} `json:"address"`
	} `json:"jobs"`
}

// Fetch implements Board.
func (a *Ashby) Fetch(ctx context.Context, token string) ([]Posting, error) {
	if !ValidToken(token) {
		return nil, fmt.Errorf("boards: ashby: invalid organization token %q", token)
	}
	body, err := getJSON(ctx, a.client, a.Name(), a.baseURL+"/posting-api/job-board/"+url.PathEscape(token))
	if err != nil {
		return nil, err
	}
	defer func() { _ = body.Close() }()

	var resp ashbyResponse
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		return nil, fmt.Errorf("boards: ashby: decode %s: %w", token, err)
	}

	out := make([]Posting, 0, len(resp.Jobs))
	for _, j := range resp.Jobs {
		if j.ID == "" || strings.TrimSpace(j.Title) == "" {
			continue
		}
		if j.IsListed != nil && !*j.IsListed {
			continue // unlisted postings aren't meant to be public
		}
		parts := []string{j.Location}
		for _, s := range j.SecondaryLocations {
			parts = append(parts, s.Location)
		}
		parts = append(parts, j.Address.PostalAddress.AddressCountry)
		if j.IsRemote {
			parts = append(parts, "Remote")
		}
		out = append(out, Posting{
			ExternalID:  j.ID,
			Title:       strings.TrimSpace(j.Title),
			Location:    joinLocations(parts...),
			URL:         j.JobURL,
			PublishedAt: parseTime(j.PublishedAt),
		})
	}
	return out, nil
}

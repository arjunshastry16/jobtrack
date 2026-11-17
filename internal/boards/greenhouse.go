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

// Greenhouse reads Greenhouse's public Job Board API:
//
//	GET https://boards-api.greenhouse.io/v1/boards/{token}/jobs
//
// No authentication is needed for published postings.
type Greenhouse struct {
	baseURL string
	client  *http.Client
}

// NewGreenhouse returns a Greenhouse board client. A nil client gets a default with a
// 30s timeout.
func NewGreenhouse(client *http.Client) *Greenhouse {
	return NewGreenhouseAt("https://boards-api.greenhouse.io", client)
}

// NewGreenhouseAt is NewGreenhouse with a different API base URL (tests use a local server).
func NewGreenhouseAt(baseURL string, client *http.Client) *Greenhouse {
	return &Greenhouse{baseURL: baseURL, client: defaultClient(client)}
}

// Name implements Board.
func (g *Greenhouse) Name() string { return "greenhouse" }

// greenhouseResponse is the subset of the API response we use. IDs are larger than
// 32 bits (e.g. 8638232002), so they decode into int64.
type greenhouseResponse struct {
	Jobs []struct {
		ID             int64  `json:"id"`
		Title          string `json:"title"`
		AbsoluteURL    string `json:"absolute_url"`
		UpdatedAt      string `json:"updated_at"`
		FirstPublished string `json:"first_published"`
		Location       struct {
			Name string `json:"name"`
		} `json:"location"`
	} `json:"jobs"`
}

// Fetch implements Board.
func (g *Greenhouse) Fetch(ctx context.Context, token string) ([]Posting, error) {
	if !ValidToken(token) {
		return nil, fmt.Errorf("boards: greenhouse: invalid board token %q", token)
	}
	body, err := getJSON(ctx, g.client, g.Name(), g.baseURL+"/v1/boards/"+url.PathEscape(token)+"/jobs")
	if err != nil {
		return nil, err
	}
	defer func() { _ = body.Close() }()

	var resp greenhouseResponse
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		return nil, fmt.Errorf("boards: greenhouse: decode %s: %w", token, err)
	}

	out := make([]Posting, 0, len(resp.Jobs))
	for _, j := range resp.Jobs {
		if j.ID == 0 || strings.TrimSpace(j.Title) == "" {
			continue // malformed entry: skip it rather than fail the whole board
		}
		out = append(out, Posting{
			ExternalID:  strconv.FormatInt(j.ID, 10),
			Title:       strings.TrimSpace(j.Title),
			Location:    strings.TrimSpace(j.Location.Name),
			URL:         j.AbsoluteURL,
			PublishedAt: parseTime(j.FirstPublished),
			UpdatedAt:   parseTime(j.UpdatedAt),
		})
	}
	return out, nil
}

// parseTime parses an RFC 3339 timestamp, returning the zero time if it is missing or bad.
func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

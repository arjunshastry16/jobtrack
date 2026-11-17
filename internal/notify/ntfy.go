package notify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// maxBodyBytes is ntfy's message size limit; longer bodies are truncated.
	maxBodyBytes = 4096
	// minPublicTopicLen: topics on the public ntfy.sh server are readable by anyone who
	// guesses the name, so require a long, random-looking one there.
	minPublicTopicLen = 16
	defaultTimeout    = 10 * time.Second
)

// Ntfy sends messages to one ntfy topic.
type Ntfy struct {
	topicURL string
	token    string
	client   *http.Client
}

// NewNtfy returns a Notifier that posts to topicURL (e.g. https://ntfy.sh/my-long-random-topic).
// token is an optional access token for protected topics. A nil client gets a default
// one with a 10s timeout.
func NewNtfy(topicURL, token string, client *http.Client) (*Ntfy, error) {
	u, err := url.Parse(topicURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, fmt.Errorf("notify: ntfy URL must be http(s)://host/topic, got %q", topicURL)
	}
	topic := strings.Trim(u.Path, "/")
	if topic == "" || strings.Contains(topic, "/") {
		return nil, fmt.Errorf("notify: ntfy URL must name exactly one topic, got %q", topicURL)
	}
	if u.Hostname() == "ntfy.sh" && len(topic) < minPublicTopicLen {
		return nil, fmt.Errorf("notify: topics on public ntfy.sh are readable by anyone who guesses them; "+
			"use a random topic of at least %d characters", minPublicTopicLen)
	}
	if client == nil {
		client = &http.Client{Timeout: defaultTimeout}
	}
	return &Ntfy{topicURL: u.String(), token: token, client: client}, nil
}

// StatusError is returned when the ntfy server answers with a non-2xx status.
type StatusError struct {
	Code int
	Body string // start of the response body, for diagnosis
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("notify: ntfy returned %d: %s", e.Code, e.Body)
}

// Retryable reports whether trying again later might succeed: rate limiting and server
// errors yes; other client errors (bad token, forbidden topic) no.
func (e *StatusError) Retryable() bool {
	return e.Code == http.StatusTooManyRequests || e.Code >= 500
}

// IsRetryable reports whether err is worth retrying. Network errors and timeouts are;
// a StatusError says for itself.
func IsRetryable(err error) bool {
	var se *StatusError
	if errors.As(err, &se) {
		return se.Retryable()
	}
	return err != nil
}

// Notify implements Notifier: one POST, message text as the body, metadata as headers.
func (n *Ntfy) Notify(ctx context.Context, m Message) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.topicURL, strings.NewReader(truncateUTF8(m.Body, maxBodyBytes)))
	if err != nil {
		return fmt.Errorf("notify: build request: %w", err)
	}
	if t := headerValue(m.Title); t != "" {
		req.Header.Set("Title", t)
	}
	if m.Priority != "" {
		req.Header.Set("Priority", string(m.Priority))
	}
	if len(m.Tags) > 0 {
		req.Header.Set("Tags", headerValue(strings.Join(m.Tags, ",")))
	}
	if m.ClickURL != "" {
		req.Header.Set("Click", headerValue(m.ClickURL))
	}
	if n.token != "" {
		req.Header.Set("Authorization", "Bearer "+n.token)
	}

	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("notify: send to ntfy: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		excerpt, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return &StatusError{Code: resp.StatusCode, Body: strings.TrimSpace(string(excerpt))}
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10)) // let the connection be reused
	return nil
}

// headerValue makes s safe for an HTTP header: line breaks and other whitespace runs
// become single spaces (no header injection), and non-ASCII text such as "Société
// Générale" is RFC 2047-encoded, which ntfy decodes.
func headerValue(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	return mime.QEncoding.Encode("utf-8", s) // returns s unchanged if it is plain ASCII
}

// truncateUTF8 shortens s to at most n bytes without splitting a character.
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	// Cutting may leave a partial character at the end; ToValidUTF8 drops it (and any
	// other invalid bytes), so the result is valid and still at most n bytes.
	return strings.ToValidUTF8(s[:n], "")
}

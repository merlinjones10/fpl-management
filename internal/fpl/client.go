package fpl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	baseURL = "https://fantasy.premierleague.com/api"

	// FPL's CDN intermittently 403s the default Go user agent.
	userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
		"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0 Safari/537.36"

	maxAttempts = 4
	maxPages    = 20
)

// ErrNotFound distinguishes "no such league" from a transient upstream failure.
var ErrNotFound = errors.New("fpl: not found")

type Client struct {
	http    *http.Client
	baseURL string
}

type Option func(*Client)

func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }
func WithBaseURL(u string) Option          { return func(c *Client) { c.baseURL = u } }

func New(opts ...Option) *Client {
	c := &Client{
		http:    &http.Client{Timeout: 20 * time.Second},
		baseURL: baseURL,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

type validator interface{ Validate() error }

func getJSON[T validator](ctx context.Context, c *Client, path string, out *T) error {
	var lastErr error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			// FPL goes read-only during price changes (~02:00 UK) and gameweek processing.
			backoff := time.Duration(250*(1<<(attempt-1))) * time.Millisecond
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
		}

		err := c.do(ctx, path, out)
		if err == nil {
			return (*out).Validate()
		}
		lastErr = err

		// A 4xx other than 429 is a real answer; retrying will not change it.
		var he *httpError
		if errors.Is(err, ErrNotFound) ||
			(errors.As(err, &he) && he.status < 500 && he.status != http.StatusTooManyRequests) {
			return err
		}
	}

	return fmt.Errorf("fpl: %s failed after %d attempts: %w", path, maxAttempts, lastErr)
}

type httpError struct {
	status int
	path   string
	ray    string // Cloudflare's Cf-Ray, set when the refusal came from the CDN
	body   string // one-line, truncated response body
}

func (e *httpError) Error() string {
	msg := fmt.Sprintf("fpl: %s returned %d", e.path, e.status)
	if e.ray != "" {
		msg += " (cf-ray " + e.ray + ")"
	}
	if e.body != "" {
		msg += ": " + e.body
	}
	return msg
}

// snippet flattens a body to one truncated line. A CDN block page is multi-line
// HTML, and a CloudWatch log entry per line is unreadable.
func snippet(b []byte) string {
	s := strings.Join(strings.Fields(string(b)), " ")
	if r := []rune(s); len(r) > 200 {
		s = string(r[:200]) + "…"
	}
	return s
}

func (c *Client) do(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")

	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%w: %s", ErrNotFound, path)
	}
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 4<<10))
		return &httpError{
			status: res.StatusCode,
			path:   path,
			ray:    res.Header.Get("Cf-Ray"),
			body:   snippet(b),
		}
	}

	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		return fmt.Errorf("fpl: decoding %s: %w", path, err)
	}
	return nil
}

// Events fetches the gameweek calendar. The bootstrap payload is ~1.6MB and
// almost all of it is player data we discard.
func (c *Client) Events(ctx context.Context) ([]Event, error) {
	var b Bootstrap
	if err := getJSON(ctx, c, "/bootstrap-static/", &b); err != nil {
		return nil, err
	}
	return b.Events, nil
}

// Standings fetches every page of a classic league's table. Works on private
// leagues: membership is not checked, only the league ID.
func (c *Client) Standings(ctx context.Context, leagueID int) (*StandingsResponse, error) {
	var first StandingsResponse
	path := fmt.Sprintf("/leagues-classic/%d/standings/?page_standings=1&page_new_entries=1", leagueID)
	if err := getJSON(ctx, c, path, &first); err != nil {
		return nil, err
	}

	rows := first.Standings.Results
	hasNext := first.Standings.HasNext

	for pageNum := 2; hasNext && pageNum <= maxPages; pageNum++ {
		var next StandingsResponse
		p := fmt.Sprintf("/leagues-classic/%d/standings/?page_standings=%d", leagueID, pageNum)
		if err := getJSON(ctx, c, p, &next); err != nil {
			return nil, err
		}
		rows = append(rows, next.Standings.Results...)
		hasNext = next.Standings.HasNext
	}

	first.Standings.Results = rows
	return &first, nil
}

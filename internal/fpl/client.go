package fpl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
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
	log     *slog.Logger
}

type Option func(*Client)

func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }
func WithBaseURL(u string) Option          { return func(c *Client) { c.baseURL = u } }
func WithLogger(log *slog.Logger) Option   { return func(c *Client) { c.log = log } }

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
	status    int
	path      string
	ray       string // Cloudflare's Cf-Ray, set when the refusal came from the CDN
	server    string // Server, "cloudflare" when the CDN answered rather than FPL
	mitigated string // Cf-Mitigated, "challenge" when a bot rule fired
	bodyLen   int
	body      string // one-line, truncated response body
}

// recordBootstrap emits one CloudWatch Embedded Metric Format event for each
// completed calendar fetch. The log's timestamp gives the precise failure time;
// the two metrics make success rate and latency graphable without a separate
// metrics dependency.
func (c *Client) recordBootstrap(started time.Time, err error) {
	if c.log == nil {
		return
	}

	outcome := "success"
	status := http.StatusOK
	if err != nil {
		outcome = "failure"
		status = 0
		var he *httpError
		if errors.As(err, &he) {
			status = he.status
		}
	}

	now := time.Now()
	attrs := []slog.Attr{
		slog.Any("_aws", map[string]any{
			"Timestamp": now.UnixMilli(),
			"CloudWatchMetrics": []any{map[string]any{
				"Namespace":  "FPLLeagueBot",
				"Dimensions": [][]string{{"endpoint", "outcome", "http_status"}},
				"Metrics": []map[string]string{
					{"Name": "BootstrapFetches", "Unit": "Count"},
					{"Name": "BootstrapFetchDuration", "Unit": "Milliseconds"},
				},
			}},
		}),
		slog.String("endpoint", "bootstrap-static"),
		slog.String("outcome", outcome),
		slog.String("http_status", strconv.Itoa(status)),
		slog.Int("bootstrap_fetches", 1),
		slog.Float64("bootstrap_fetch_duration", float64(now.Sub(started))/float64(time.Millisecond)),
	}
	if err != nil {
		attrs = append(attrs, slog.String("err", err.Error()))
	}
	c.log.LogAttrs(context.Background(), slog.LevelInfo, "fpl bootstrap fetch", attrs...)
}

func (e *httpError) Error() string {
	// The tail is unconditional. An absent ray and an empty body are themselves
	// the diagnosis, and omitting them makes this indistinguishable from the
	// bare status line an older build emitted.
	return fmt.Sprintf("fpl: %s returned %d [cf-ray=%q server=%q mitigated=%q body=%dB] %s",
		e.path, e.status, e.ray, e.server, e.mitigated, e.bodyLen, e.body)
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
			status:    res.StatusCode,
			path:      path,
			ray:       res.Header.Get("Cf-Ray"),
			server:    res.Header.Get("Server"),
			mitigated: res.Header.Get("Cf-Mitigated"),
			bodyLen:   len(b),
			body:      snippet(b),
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
	started := time.Now()
	var b Bootstrap
	if err := getJSON(ctx, c, "/bootstrap-static/", &b); err != nil {
		c.recordBootstrap(started, err)
		return nil, err
	}
	c.recordBootstrap(started, nil)
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

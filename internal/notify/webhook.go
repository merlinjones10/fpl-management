package notify

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// webhookAttempts bounds how many times one part is posted. Leagues can share a
// webhook, so a single tick can put several posts through the same bucket back
// to back; both APIs answer that with a sub-second Retry-After, and a couple of
// attempts clears it. Past that the far end is not merely busy.
const webhookAttempts = 3

// defaultRetryAfter covers a 429 that carries no usable header. It matches the
// first backoff step in internal/fpl/client.go.
const defaultRetryAfter = 250 * time.Millisecond

// maxRetryAfter refuses to wait out an implausible delay. Every league shares
// one 60s Lambda timeout, so parking the tick for a minute to satisfy one
// message would cost the other leagues theirs.
const maxRetryAfter = 5 * time.Second

// postWebhook posts one payload to an incoming webhook, retrying while the far
// end asks it to slow down.
//
// Only 429 is retried, deliberately. A rate limit is a guaranteed rejection —
// nothing was posted, so trying again cannot duplicate anything. A 5xx carries
// no such promise: the far end may have accepted the message before failing to
// say so, and retrying would post it twice.
//
// This matters because leagues can share a webhook. Reminder and digest for
// several leagues land back to back in one tick, which is exactly the burst
// these per-webhook buckets answer with a 429 — and without this a rate limit
// reads as a hard failure, app.send releases the claim, and the next tick
// re-sends every part of the message including the ones that already arrived.
func postWebhook(ctx context.Context, client *http.Client, url string, payload []byte) error {
	for attempt := 1; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
		if err != nil {
			return scrubURL(err)
		}
		req.Header.Set("Content-Type", "application/json")

		res, err := client.Do(req)
		if err != nil {
			return scrubURL(err)
		}

		if res.StatusCode != http.StatusTooManyRequests || attempt == webhookAttempts {
			err := webhookStatus(res)
			res.Body.Close()
			return err
		}

		wait, ok := retryAfter(res)
		res.Body.Close()
		if !ok {
			return fmt.Errorf("status 429, asked to wait longer than %s", maxRetryAfter)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}

// retryAfter reads the delay a 429 asks for. Both APIs send Retry-After in
// seconds, Discord sometimes fractional. Anything unreadable — including the
// HTTP-date form neither of them uses — falls back to a short wait rather than
// abandoning a request that would probably succeed.
func retryAfter(res *http.Response) (time.Duration, bool) {
	raw := strings.TrimSpace(res.Header.Get("Retry-After"))
	if raw == "" {
		return defaultRetryAfter, true
	}

	secs, err := strconv.ParseFloat(raw, 64)
	if err != nil || secs < 0 {
		return defaultRetryAfter, true
	}

	wait := time.Duration(secs * float64(time.Second))
	if wait > maxRetryAfter {
		return 0, false
	}
	return wait, true
}

// webhookStatus turns a non-2xx into an error carrying the far end's reason.
// Discord answers a good post with 204 and an empty body, Slack with 200 and
// "ok", so the status alone decides; the body is read only to explain a failure.
func webhookStatus(res *http.Response) error {
	if res.StatusCode >= 200 && res.StatusCode <= 299 {
		return nil
	}

	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(res.Body); err != nil {
		return fmt.Errorf("status %d, unreadable response: %w", res.StatusCode, err)
	}
	// The URL is a secret; only the response body goes into the error.
	return fmt.Errorf("status %d: %s", res.StatusCode, truncate(strings.TrimSpace(buf.String()), 200))
}

package notify

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"fplbot/internal/digest"
)

// rateLimitServer answers the first `limited` requests with 429 and everything
// after with a success, counting what arrived.
func rateLimitServer(t *testing.T, limited int, retryAfter, okBody string, okStatus int) (*httptest.Server, *int) {
	t.Helper()
	var calls int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls <= limited {
			if retryAfter != "" {
				w.Header().Set("Retry-After", retryAfter)
			}
			w.WriteHeader(http.StatusTooManyRequests)
			io.WriteString(w, `{"message":"You are being rate limited."}`)
			return
		}
		w.WriteHeader(okStatus)
		io.WriteString(w, okBody)
	}))
	t.Cleanup(srv.Close)

	return srv, &calls
}

// Leagues sharing one webhook stack their posts into a single bucket, so a 429
// is an expected answer rather than a failure. It must not surface as one.
func TestDiscordRetriesARateLimit(t *testing.T) {
	srv, calls := rateLimitServer(t, 1, "0", "", http.StatusNoContent)
	dc := NewDiscord(srv.URL + "/webhooks/1/tok")

	if err := dc.Send(context.Background(), digest.Message{Body: "hi"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if *calls != 2 {
		t.Errorf("server saw %d requests, want the 429 retried once", *calls)
	}
}

func TestSlackRetriesARateLimit(t *testing.T) {
	srv, calls := rateLimitServer(t, 1, "0", "ok", http.StatusOK)
	sl := NewSlack(srv.URL + "/services/T/B/tok")

	if err := sl.Send(context.Background(), digest.Message{Body: "hi"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if *calls != 2 {
		t.Errorf("server saw %d requests, want the 429 retried once", *calls)
	}
}

// The real shape of the problem: a multi-part digest where the bucket rejects a
// part partway through. Every part must still land, or app.send releases the
// claim and the next tick re-posts the ones that already arrived.
func TestRateLimitMidMessageStillDeliversEveryPart(t *testing.T) {
	var calls, delivered int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		// Reject the second request once, as a bucket would mid-burst.
		if calls == 2 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		delivered++
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	dc := NewDiscord(srv.URL + "/webhooks/1/tok")

	var b strings.Builder
	for i := 0; i < 400; i++ {
		b.WriteString("12. Some Team Name\n")
	}

	if err := dc.Send(context.Background(), digest.Message{Body: b.String()}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if delivered < 4 {
		t.Errorf("delivered %d parts of a 4-part message", delivered)
	}
}

func TestGivesUpAfterRepeatedRateLimits(t *testing.T) {
	srv, calls := rateLimitServer(t, 99, "0", "", http.StatusNoContent)
	dc := NewDiscord(srv.URL + "/webhooks/1/tok")

	err := dc.Send(context.Background(), digest.Message{Body: "hi"})

	if err == nil {
		t.Fatal("Send error = nil, want the rate limit surfaced once it will not clear")
	}
	if *calls != webhookAttempts {
		t.Errorf("server saw %d requests, want %d", *calls, webhookAttempts)
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("error = %v, want it to name the status", err)
	}
}

// Every league shares one Lambda timeout, so a long Retry-After is refused
// rather than waited out — one league must not spend the budget for all of them.
func TestRefusesAnImplausibleRetryAfter(t *testing.T) {
	srv, calls := rateLimitServer(t, 99, "600", "", http.StatusNoContent)
	dc := NewDiscord(srv.URL + "/webhooks/1/tok")

	err := dc.Send(context.Background(), digest.Message{Body: "hi"})

	if err == nil {
		t.Fatal("Send error = nil, want the wait refused")
	}
	if *calls != 1 {
		t.Errorf("server saw %d requests, want it to give up immediately", *calls)
	}
}

// A 5xx may mean the far end accepted the message and then failed to say so.
// Retrying would post it twice, which is worse than reporting the failure.
func TestDoesNotRetryAServerError(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadGateway)
		io.WriteString(w, "upstream is unwell")
	}))
	t.Cleanup(srv.Close)

	dc := NewDiscord(srv.URL + "/webhooks/1/tok")

	err := dc.Send(context.Background(), digest.Message{Body: "hi"})

	if err == nil {
		t.Fatal("Send error = nil, want the 502 surfaced")
	}
	if calls != 1 {
		t.Errorf("server saw %d requests, want no retry on a 5xx", calls)
	}
}

// A rate limit must not leak the token either — the retry path builds a fresh
// request each attempt, so it has its own chance to.
func TestRateLimitErrorOmitsWebhookURL(t *testing.T) {
	srv, _ := rateLimitServer(t, 99, "0", "", http.StatusNoContent)
	dc := NewDiscord(srv.URL + "/webhooks/1/super-secret-token")

	err := dc.Send(context.Background(), digest.Message{Body: "hi"})
	if err == nil {
		t.Fatal("Send error = nil, want failure")
	}
	if strings.Contains(err.Error(), "super-secret-token") {
		t.Errorf("error leaks the webhook token: %v", err)
	}
}

func TestRetryAfterParsing(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   time.Duration
		ok     bool
	}{
		{"absent", "", defaultRetryAfter, true},
		{"whole seconds", "2", 2 * time.Second, true},
		{"fractional, as discord sends", "0.75", 750 * time.Millisecond, true},
		{"zero", "0", 0, true},
		{"unparseable falls back", "Wed, 21 Oct 2026 07:28:00 GMT", defaultRetryAfter, true},
		{"negative falls back", "-5", defaultRetryAfter, true},
		{"longer than the cap is refused", "600", 0, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := &http.Response{Header: http.Header{}}
			if tc.header != "" {
				res.Header.Set("Retry-After", tc.header)
			}

			got, ok := retryAfter(res)

			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if got != tc.want {
				t.Errorf("wait = %v, want %v", got, tc.want)
			}
		})
	}
}

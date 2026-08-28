package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"fplbot/internal/digest"
)

func discordServer(t *testing.T, status int, body string) (*httptest.Server, *[]discordPayload) {
	t.Helper()
	var got []discordPayload

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q", ct)
		}

		var payload discordPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode payload: %v", err)
		}
		got = append(got, payload)

		w.WriteHeader(status)
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)

	return srv, &got
}

func TestDiscordPostsBodyWithMarkupIntact(t *testing.T) {
	srv, got := discordServer(t, http.StatusNoContent, "")
	dc := NewDiscord(srv.URL + "/webhooks/1/tok")

	err := dc.Send(context.Background(), digest.Message{
		Subject: "ignored by discord",
		Body:    "*Leeds Legends* — Gameweek 3\n1. Three-o Walcott — 200 _(+61)_",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	if len(*got) != 1 {
		t.Fatalf("got %d requests, want 1", len(*got))
	}
	payload := (*got)[0]

	// The markup must reach Discord untouched: Copy Text returns the raw source,
	// which is what gets pasted into WhatsApp.
	if !strings.HasPrefix(payload.Content, "*Leeds Legends*") {
		t.Errorf("content = %q", payload.Content)
	}
	if !strings.Contains(payload.Content, "_(+61)_") {
		t.Errorf("italic markup lost: %q", payload.Content)
	}
	if strings.Contains(payload.Content, "ignored by discord") {
		t.Error("subject leaked into the message body")
	}
	if payload.Flags&discordSuppressEmbeds == 0 {
		t.Error("SUPPRESS_EMBEDS not set; a link preview would be copied with the body")
	}
	// A manager can name their team "@everyone".
	if len(payload.AllowedMentions.Parse) != 0 {
		t.Errorf("allowed_mentions.parse = %v, want empty", payload.AllowedMentions.Parse)
	}
}

func TestDiscordSurfacesHTTPError(t *testing.T) {
	srv, _ := discordServer(t, http.StatusNotFound, `{"message":"Unknown Webhook","code":10015}`)
	dc := NewDiscord(srv.URL + "/webhooks/1/gone")

	err := dc.Send(context.Background(), digest.Message{Body: "hi"})

	if err == nil {
		t.Fatal("Send error = nil, want failure")
	}
	if !strings.Contains(err.Error(), "Unknown Webhook") {
		t.Errorf("error = %v, want the API message", err)
	}
}

// The webhook token sits in the URL path, so it must never reach an error
// string that ends up in CloudWatch.
func TestDiscordErrorOmitsWebhookURL(t *testing.T) {
	srv, _ := discordServer(t, http.StatusForbidden, `{"message":"Forbidden"}`)
	dc := NewDiscord(srv.URL + "/webhooks/1/super-secret-token")

	err := dc.Send(context.Background(), digest.Message{Body: "hi"})
	if err == nil {
		t.Fatal("Send error = nil, want failure")
	}
	if strings.Contains(err.Error(), "super-secret-token") {
		t.Errorf("error leaks the webhook token: %v", err)
	}
}

// net/http records the request URL in *url.Error, so a connection failure —
// not just an API rejection — would otherwise print the token to CloudWatch.
func TestDiscordTransportErrorOmitsWebhookURL(t *testing.T) {
	srv, _ := discordServer(t, http.StatusNoContent, "")
	url := srv.URL + "/webhooks/1/super-secret-token"
	srv.Close() // nothing is listening, so Do fails at the transport

	dc := NewDiscord(url)

	err := dc.Send(context.Background(), digest.Message{Body: "hi"})
	if err == nil {
		t.Fatal("Send error = nil, want a transport failure")
	}
	if strings.Contains(err.Error(), "super-secret-token") {
		t.Errorf("error leaks the webhook token: %v", err)
	}
}

func TestDiscordSplitsOversizeMessage(t *testing.T) {
	srv, got := discordServer(t, http.StatusNoContent, "")
	dc := NewDiscord(srv.URL + "/webhooks/1/tok")

	// 200 lines of ~19 runes comfortably exceeds the 2000 limit.
	var b strings.Builder
	for i := 0; i < 200; i++ {
		b.WriteString("12. Some Team Name\n")
	}

	if err := dc.Send(context.Background(), digest.Message{Body: b.String()}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if len(*got) < 2 {
		t.Fatalf("got %d requests, want the message split", len(*got))
	}
	for i, payload := range *got {
		if n := len([]rune(payload.Content)); n > discordMaxRunes {
			t.Errorf("part %d is %d runes, over the %d limit", i+1, n, discordMaxRunes)
		}
		// Splitting on line boundaries keeps table rows intact.
		if strings.HasPrefix(payload.Content, "Some Team") {
			t.Errorf("part %d starts mid-row: %q", i+1, payload.Content[:20])
		}
	}
}

// A part that fails must say so, since the earlier parts are already posted.
func TestDiscordReportsWhichPartFailed(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"message":"nope"}`)
	}))
	t.Cleanup(srv.Close)

	dc := NewDiscord(srv.URL + "/webhooks/1/tok")

	var b strings.Builder
	for i := 0; i < 200; i++ {
		b.WriteString("12. Some Team Name\n")
	}

	err := dc.Send(context.Background(), digest.Message{Body: b.String()})
	if err == nil {
		t.Fatal("Send error = nil, want failure")
	}
	if !strings.Contains(err.Error(), "part 2/") {
		t.Errorf("error = %v, want it to name the failing part", err)
	}
}

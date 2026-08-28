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

func slackServer(t *testing.T, status int, body string) (*httptest.Server, *[]slackPayload) {
	t.Helper()
	var got []slackPayload

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q", ct)
		}

		var payload slackPayload
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

func TestSlackPostsBodyAsMrkdwn(t *testing.T) {
	srv, got := slackServer(t, http.StatusOK, "ok")
	sl := NewSlack(srv.URL + "/services/T/B/tok")

	err := sl.Send(context.Background(), digest.Message{
		Subject: "ignored by slack",
		Body:    "*Leeds Legends* — Gameweek 3\n1. Three-o Walcott — 200 _(+61)_",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	if len(*got) != 1 {
		t.Fatalf("got %d requests, want 1", len(*got))
	}
	payload := (*got)[0]

	// mrkdwn stays on: Slack reads *bold* and _italic_ the same way the bodies
	// are written, so the channel renders them properly.
	if !strings.HasPrefix(payload.Text, "*Leeds Legends*") {
		t.Errorf("text = %q", payload.Text)
	}
	if !strings.Contains(payload.Text, "_(+61)_") {
		t.Errorf("italic markup lost: %q", payload.Text)
	}
	if strings.Contains(payload.Text, "ignored by slack") {
		t.Error("subject leaked into the message body")
	}
	if payload.UnfurlLinks || payload.UnfurlMedia {
		t.Error("unfurling is on; the reminder's link would expand into a preview card")
	}
}

// Team names come from managers, and Slack reserves &, < and >. Left raw, a
// name is mangled or swallowed — and <!everyone> would ping the channel.
func TestSlackEscapesReservedCharacters(t *testing.T) {
	srv, got := slackServer(t, http.StatusOK, "ok")
	sl := NewSlack(srv.URL + "/services/T/B/tok")

	body := "1. Salah & Co — 200\n2. <Wildcard> — 190\n3. <!everyone> — 180"
	if err := sl.Send(context.Background(), digest.Message{Body: body}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	text := (*got)[0].Text
	want := "1. Salah &amp; Co — 200\n2. &lt;Wildcard&gt; — 190\n3. &lt;!everyone&gt; — 180"
	if text != want {
		t.Errorf("text = %q, want %q", text, want)
	}
}

func TestSlackSurfacesHTTPError(t *testing.T) {
	srv, _ := slackServer(t, http.StatusNotFound, "no_service")
	sl := NewSlack(srv.URL + "/services/T/B/gone")

	err := sl.Send(context.Background(), digest.Message{Body: "hi"})

	if err == nil {
		t.Fatal("Send error = nil, want failure")
	}
	if !strings.Contains(err.Error(), "no_service") {
		t.Errorf("error = %v, want the API message", err)
	}
}

// The webhook token sits in the URL path, so it must never reach an error
// string that ends up in CloudWatch.
func TestSlackErrorOmitsWebhookURL(t *testing.T) {
	srv, _ := slackServer(t, http.StatusForbidden, "invalid_token")
	sl := NewSlack(srv.URL + "/services/T/B/super-secret-token")

	err := sl.Send(context.Background(), digest.Message{Body: "hi"})
	if err == nil {
		t.Fatal("Send error = nil, want failure")
	}
	if strings.Contains(err.Error(), "super-secret-token") {
		t.Errorf("error leaks the webhook token: %v", err)
	}
}

// net/http records the request URL in *url.Error, so a connection failure —
// not just an API rejection — would otherwise print the token to CloudWatch.
func TestSlackTransportErrorOmitsWebhookURL(t *testing.T) {
	srv, _ := slackServer(t, http.StatusOK, "ok")
	url := srv.URL + "/services/T/B/super-secret-token"
	srv.Close() // nothing is listening, so Do fails at the transport

	sl := NewSlack(url)

	err := sl.Send(context.Background(), digest.Message{Body: "hi"})
	if err == nil {
		t.Fatal("Send error = nil, want a transport failure")
	}
	if strings.Contains(err.Error(), "super-secret-token") {
		t.Errorf("error leaks the webhook token: %v", err)
	}
}

func TestSlackSplitsOversizeMessage(t *testing.T) {
	srv, got := slackServer(t, http.StatusOK, "ok")
	sl := NewSlack(srv.URL + "/services/T/B/tok")

	// 300 lines of ~19 runes comfortably exceeds the 3000 limit.
	var b strings.Builder
	for i := 0; i < 300; i++ {
		b.WriteString("12. Some Team Name\n")
	}

	if err := sl.Send(context.Background(), digest.Message{Body: b.String()}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if len(*got) < 2 {
		t.Fatalf("got %d requests, want the message split", len(*got))
	}
	for i, payload := range *got {
		if n := len([]rune(payload.Text)); n > slackMaxRunes {
			t.Errorf("part %d is %d runes, over the %d limit", i+1, n, slackMaxRunes)
		}
		// Splitting on line boundaries keeps table rows intact.
		if strings.HasPrefix(payload.Text, "Some Team") {
			t.Errorf("part %d starts mid-row: %q", i+1, payload.Text[:20])
		}
	}
}

// Escaping expands the body, so it has to happen before the split or a part can
// land over the limit Slack actually measures.
func TestSlackSplitsAfterEscaping(t *testing.T) {
	srv, got := slackServer(t, http.StatusOK, "ok")
	sl := NewSlack(srv.URL + "/services/T/B/tok")

	// Each line is 11 runes raw and 23 escaped, so 200 of them sit inside the
	// 3000 limit unescaped and well past it once escaped.
	var b strings.Builder
	for i := 0; i < 200; i++ {
		b.WriteString("1. A&B&C&D\n")
	}

	if err := sl.Send(context.Background(), digest.Message{Body: b.String()}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if len(*got) < 2 {
		t.Fatalf("got %d requests, want the escaped body split", len(*got))
	}
	for i, payload := range *got {
		if n := len([]rune(payload.Text)); n > slackMaxRunes {
			t.Errorf("part %d is %d runes, over the %d limit", i+1, n, slackMaxRunes)
		}
	}
}

// A part that fails must say so, since the earlier parts are already posted.
func TestSlackReportsWhichPartFailed(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusOK)
			io.WriteString(w, "ok")
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, "invalid_payload")
	}))
	t.Cleanup(srv.Close)

	sl := NewSlack(srv.URL + "/services/T/B/tok")

	var b strings.Builder
	for i := 0; i < 300; i++ {
		b.WriteString("12. Some Team Name\n")
	}

	err := sl.Send(context.Background(), digest.Message{Body: b.String()})
	if err == nil {
		t.Fatal("Send error = nil, want failure")
	}
	if !strings.Contains(err.Error(), "part 2/") {
		t.Errorf("error = %v, want it to name the failing part", err)
	}
}

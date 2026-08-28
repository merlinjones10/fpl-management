package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"fplbot/internal/digest"
)

// Slack delivers via an incoming webhook: one channel, one secret, no bot token
// and no OAuth scopes to keep in step with.
//
// Unlike the Discord channel, this one is read in Slack rather than copied on
// into WhatsApp, so mrkdwn is left on and the body needs no translation —
// Slack's *bold*, _italic_ and bare-URL autolinking already match the markup
// digest renders.
type Slack struct {
	http *http.Client
	// webhookURL carries the webhook token in its path, so it is a secret and
	// never logged.
	webhookURL string
}

// slackMaxRunes bounds one post. The hard limit on the text field is 40000, but
// Slack's own guidance is to split anything past 4000; 3000 leaves margin.
const slackMaxRunes = 3000

// slackEscaper escapes the three characters Slack reserves in message text.
// Team and manager names are user-supplied, so a league holding "Salah & Co" or
// "<Wildcard>" would otherwise render wrong or lose the name entirely. Escaping
// < also means no team name can spell a broadcast mention like <!everyone>,
// which is the only form Slack actually pings on.
var slackEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

func NewSlack(webhookURL string, opts ...SlackOption) *Slack {
	s := &Slack{
		http:       &http.Client{Timeout: 15 * time.Second},
		webhookURL: strings.TrimSuffix(strings.TrimSpace(webhookURL), "/"),
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

type SlackOption func(*Slack)

func WithSlackHTTPClient(h *http.Client) SlackOption {
	return func(s *Slack) { s.http = h }
}

// Send posts the message body. Message.Subject is dropped: Slack has no subject
// line, and every body already opens with its own heading.
func (s *Slack) Send(ctx context.Context, msg digest.Message) error {
	// Escape before splitting, so the limit is measured against what is actually
	// posted — an escaped & is five runes, not one.
	parts := splitMessage(slackEscaper.Replace(msg.Body), slackMaxRunes)

	for i, part := range parts {
		if err := s.sendOne(ctx, part); err != nil {
			// Earlier parts are already delivered; there is no way to unsend
			// them, so say which part failed rather than implying nothing went.
			return fmt.Errorf("slack part %d/%d: %w", i+1, len(parts), err)
		}
	}
	return nil
}

// slackPayload leaves mrkdwn on — the point of this transport is that the
// channel reads well. The unfurl flags are the SUPPRESS_EMBEDS equivalent: the
// reminder carries a link, and a preview card adds nothing to a two-line
// message.
type slackPayload struct {
	Text        string `json:"text"`
	UnfurlLinks bool   `json:"unfurl_links"`
	UnfurlMedia bool   `json:"unfurl_media"`
}

func (s *Slack) sendOne(ctx context.Context, text string) error {
	payload, err := json.Marshal(slackPayload{Text: text})
	if err != nil {
		return err
	}
	return postWebhook(ctx, s.http, s.webhookURL, payload)
}

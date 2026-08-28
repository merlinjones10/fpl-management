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

// Discord delivers via an incoming webhook, which posts to one channel and
// needs no bot, no gateway connection and no per-guild permissions.
//
// The body is posted as-is, carrying its WhatsApp markup. Discord renders
// *bold* as italic — cosmetically wrong in the channel — but the message's
// Copy Text action yields the raw source, so the markup survives the paste
// into WhatsApp. That is the whole point of this transport; wrapping the body
// in a code block to make Discord show it literally was tried and rejected as
// harder to read.
type Discord struct {
	http *http.Client
	// webhookURL carries the webhook token in its path, so it is a secret and
	// never logged.
	webhookURL string
}

// discordMaxRunes is the limit for a single webhook message's content field.
const discordMaxRunes = 2000

func NewDiscord(webhookURL string, opts ...DiscordOption) *Discord {
	d := &Discord{
		http:       &http.Client{Timeout: 15 * time.Second},
		webhookURL: strings.TrimSuffix(strings.TrimSpace(webhookURL), "/"),
	}
	for _, o := range opts {
		o(d)
	}
	return d
}

type DiscordOption func(*Discord)

func WithDiscordHTTPClient(h *http.Client) DiscordOption {
	return func(d *Discord) { d.http = h }
}

// Send posts the message body. Message.Subject is dropped: Discord has no
// subject line, and every body already opens with its own heading.
func (d *Discord) Send(ctx context.Context, msg digest.Message) error {
	parts := splitMessage(msg.Body, discordMaxRunes)

	for i, part := range parts {
		if err := d.sendOne(ctx, part); err != nil {
			// Earlier parts are already delivered; there is no way to unsend
			// them, so say which part failed rather than implying nothing went.
			return fmt.Errorf("discord part %d/%d: %w", i+1, len(parts), err)
		}
	}
	return nil
}

// discordSuppressEmbeds is the SUPPRESS_EMBEDS message flag. The reminder body
// carries a link, and an unfurled preview card would be copied along with it.
const discordSuppressEmbeds = 1 << 2

type discordPayload struct {
	Content         string                 `json:"content"`
	Flags           int                    `json:"flags"`
	AllowedMentions discordAllowedMentions `json:"allowed_mentions"`
}

// discordAllowedMentions with an empty Parse disables every mention type. Team
// names come from managers, so a body containing "@everyone" would otherwise
// ping the channel.
type discordAllowedMentions struct {
	Parse []string `json:"parse"`
}

func (d *Discord) sendOne(ctx context.Context, content string) error {
	payload, err := json.Marshal(discordPayload{
		Content:         content,
		Flags:           discordSuppressEmbeds,
		AllowedMentions: discordAllowedMentions{Parse: []string{}},
	})
	if err != nil {
		return err
	}
	return postWebhook(ctx, d.http, d.webhookURL, payload)
}

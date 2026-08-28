// Package notify delivers a rendered message. Delivery is kept behind Sender
// so a transport can be swapped or added without touching the digest logic.
package notify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"

	"fplbot/internal/digest"
)

type Sender interface {
	Send(ctx context.Context, msg digest.Message) error
}

// scrubURL drops the URL that net/http records in *url.Error. Every webhook
// transport here carries its token in that URL, and the error text ends up in
// CloudWatch.
func scrubURL(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return fmt.Errorf("%s to webhook: %w", urlErr.Op, urlErr.Err)
	}
	return err
}

// Log is the DRY_RUN sender: prints the message instead of delivering it.
type Log struct{}

func (Log) Send(_ context.Context, msg digest.Message) error {
	slog.Info("dry run, not sending", "subject", msg.Subject)
	fmt.Printf("\n--- %s ---\n%s---\n", msg.Subject, msg.Body)
	return nil
}

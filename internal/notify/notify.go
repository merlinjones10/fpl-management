// Package notify delivers a rendered message. Delivery is kept behind Sender
// so a transport can be swapped or added without touching the digest logic.
package notify

import (
	"context"
	"fmt"
	"log/slog"

	"fplbot/internal/digest"
)

type Sender interface {
	Send(ctx context.Context, msg digest.Message) error
}

// Log is the DRY_RUN sender: prints the message instead of delivering it.
type Log struct{}

func (Log) Send(_ context.Context, msg digest.Message) error {
	slog.Info("dry run, not sending", "subject", msg.Subject)
	fmt.Printf("\n--- %s ---\n%s---\n", msg.Subject, msg.Body)
	return nil
}

// Command preview runs one tick against the live FPL API and prints the
// messages instead of sending them. State is held in memory, so it always
// renders regardless of what has already been sent for real.
//
//	go run ./cmd/preview -league 1058423
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	_ "time/tzdata"

	"fplbot/internal/app"
	"fplbot/internal/config"
	"fplbot/internal/fpl"
	"fplbot/internal/notify"
	"fplbot/internal/store"
)

// memStore is a Store that forgets everything, so preview never suppresses a
// message on the grounds that it was already sent.
type memStore struct{}

func (memStore) Claim(context.Context, store.MessageKind, int) (bool, error) { return true, nil }
func (memStore) Release(context.Context, store.MessageKind, int) error       { return nil }
func (memStore) PutSnapshot(context.Context, store.Snapshot) error           { return nil }
func (memStore) LatestSnapshotBefore(context.Context, int) (*store.Snapshot, error) {
	return nil, nil
}

func main() {
	league := flag.Int("league", 0, "FPL league ID")
	lead := flag.Float64("lead-hours", 48, "reminder lead time in hours")
	tz := flag.String("tz", "Europe/London", "IANA timezone for rendering")
	flag.Parse()

	if *league <= 0 {
		fmt.Fprintln(os.Stderr, "usage: preview -league <id>")
		os.Exit(2)
	}

	loc, err := time.LoadLocation(*tz)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bad timezone %q: %v\n", *tz, err)
		os.Exit(1)
	}

	cfg := &config.Config{
		ReminderLead: time.Duration(*lead * float64(time.Hour)),
		Location:     loc,
		DryRun:       true,
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	client := fpl.New()
	a := app.New(cfg, config.League{ID: *league, Channel: config.ChannelLog},
		client, memStore{}, notify.Log{}, log)

	// One league, but through the Fleet, so preview exercises the path the
	// Lambda actually takes.
	if _, err := app.NewFleet(client, []*app.App{a}, log).Tick(context.Background(), time.Now().UTC()); err != nil {
		fmt.Fprintf(os.Stderr, "tick: %v\n", err)
		os.Exit(1)
	}
}

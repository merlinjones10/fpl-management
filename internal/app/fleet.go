package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"fplbot/internal/fpl"
)

// EventsClient is the league-independent half of the FPL API.
type EventsClient interface {
	Events(ctx context.Context) ([]fpl.Event, error)
}

// Fleet runs every league off one fetch of the gameweek calendar. That payload
// is ~1.6MB and identical whoever is asking, so fetching it per league would be
// the same bytes n times against an undocumented, unauthenticated API — which
// is the argument against a stack per league. Everything genuinely per-league
// (standings, state partition, sender) stays inside the App.
type Fleet struct {
	fpl  EventsClient
	apps []*App
	log  *slog.Logger
}

func NewFleet(client EventsClient, apps []*App, log *slog.Logger) *Fleet {
	return &Fleet{fpl: client, apps: apps, log: log}
}

// LeagueResult pairs one league's outcome with its ID, so `make invoke` shows
// which league did what.
type LeagueResult struct {
	LeagueID int
	Result
}

type FleetResult struct {
	Leagues []LeagueResult
}

// Tick reads the calendar once and runs every league against it. Failures are
// collected and joined rather than short-circuited, for the same reason the
// reminder runs before the digest: one league's broken webhook must not
// suppress another league's time-critical message.
func (f *Fleet) Tick(ctx context.Context, now time.Time) (FleetResult, error) {
	var res FleetResult

	// The one fetch every league shares. Nothing can be decided without it, so
	// this is the only failure that stops the tick outright.
	events, err := f.fpl.Events(ctx)
	if err != nil {
		return res, fmt.Errorf("fetch events: %w", err)
	}

	var errs []error
	for _, a := range f.apps {
		r, err := a.Tick(ctx, now, events)
		res.Leagues = append(res.Leagues, LeagueResult{LeagueID: a.league.ID, Result: r})
		if err != nil {
			errs = append(errs, fmt.Errorf("league %d: %w", a.league.ID, err))
		}
	}

	f.log.Info("tick complete", "leagues", len(f.apps), "failed", len(errs))
	return res, errors.Join(errs...)
}

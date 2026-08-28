// Package app holds the decision logic for one tick: given the current FPL
// state and what we have already sent, work out which messages are due.
//
// An App is one league. A Fleet is all of them, sharing a single fetch of the
// gameweek calendar — see fleet.go.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"fplbot/internal/config"
	"fplbot/internal/digest"
	"fplbot/internal/fpl"
	"fplbot/internal/notify"
	"fplbot/internal/store"
)

// StandingsClient is the per-league half of the FPL API. The other half, the
// gameweek calendar, is identical for every league and belongs to the Fleet.
type StandingsClient interface {
	Standings(ctx context.Context, leagueID int) (*fpl.StandingsResponse, error)
}

type Store interface {
	Claim(ctx context.Context, kind store.MessageKind, gw int) (bool, error)
	Release(ctx context.Context, kind store.MessageKind, gw int) error
	PutSnapshot(ctx context.Context, snap store.Snapshot) error
	LatestSnapshotBefore(ctx context.Context, gw int) (*store.Snapshot, error)
}

// App is one league: its own state partition, its own sender, its own decision
// about what is due. Several of them run off one Fleet.
type App struct {
	cfg    *config.Config
	league config.League
	fpl    StandingsClient
	store  Store
	sender notify.Sender
	log    *slog.Logger
}

func New(
	cfg *config.Config, league config.League,
	client StandingsClient, st Store, sender notify.Sender, log *slog.Logger,
) *App {
	return &App{
		cfg: cfg, league: league, fpl: client, store: st, sender: sender,
		// Attached here rather than by the caller, so every line this league
		// logs is attributable in a log group shared with the others.
		log: log.With("league", league.ID),
	}
}

type Result struct {
	ReminderSent bool
	DigestSent   bool
}

// Tick is safe to run on any schedule and as often as you like: every send is
// gated on a conditional write, so repeats and Lambda retries are no-ops.
//
// events is passed in rather than fetched: the calendar is the same bytes for
// every league, so the Fleet reads it once. See Fleet.Tick.
func (a *App) Tick(ctx context.Context, now time.Time, events []fpl.Event) (Result, error) {
	var res Result

	standings, err := a.fpl.Standings(ctx, a.league.ID)
	if err != nil {
		return res, fmt.Errorf("fetch standings: %w", err)
	}

	// The reminder is time-critical, so it goes first and a digest failure
	// cannot stop it. Both can fire in the same tick.
	var errs []error

	sent, err := a.maybeReminder(ctx, now, events, standings)
	res.ReminderSent = sent
	if err != nil {
		errs = append(errs, fmt.Errorf("reminder: %w", err))
	}

	sent, err = a.maybeDigest(ctx, now, events, standings)
	res.DigestSent = sent
	if err != nil {
		errs = append(errs, fmt.Errorf("digest: %w", err))
	}

	return res, errors.Join(errs...)
}

func (a *App) maybeReminder(
	ctx context.Context, now time.Time, events []fpl.Event, standings *fpl.StandingsResponse,
) (bool, error) {
	ev := nextDeadline(events, now)
	if ev == nil {
		a.log.Info("no upcoming deadline")
		return false, nil
	}

	deadline, err := ev.Deadline()
	if err != nil {
		return false, err
	}
	until := deadline.Sub(now)
	if until > a.cfg.ReminderLead {
		a.log.Info("deadline not due yet", "gw", ev.ID, "until", until.String())
		return false, nil
	}

	won, err := a.store.Claim(ctx, store.KindReminder, ev.ID)
	if err != nil || !won {
		return false, err
	}

	msg := digest.RenderReminder(digest.ReminderInput{
		LeagueName:   standings.League.Name,
		Event:        *ev,
		ManagerCount: managerCount(standings),
		Now:          now,
		Location:     a.cfg.Location,
	})

	if err := a.send(ctx, store.KindReminder, ev.ID, msg); err != nil {
		return false, err
	}
	a.log.Info("reminder sent", "gw", ev.ID, "deadline", deadline)
	return true, nil
}

func (a *App) maybeDigest(
	ctx context.Context, now time.Time, events []fpl.Event, standings *fpl.StandingsResponse,
) (bool, error) {
	// Before any gameweek is scored the table is empty and everyone sits in
	// new_entries. There is nothing to report, so stay quiet until GW1 lands.
	if !standings.Started() {
		a.log.Info("league has not started, no table yet",
			"joined", len(standings.NewEntries.Results))
		return false, nil
	}

	ev := latestChecked(events)
	if ev == nil {
		a.log.Info("no scored gameweek yet")
		return false, nil
	}

	won, err := a.store.Claim(ctx, store.KindDigest, ev.ID)
	if err != nil || !won {
		return false, err
	}

	previous, err := a.store.LatestSnapshotBefore(ctx, ev.ID)
	if err != nil {
		// A missing baseline is recoverable: fall back to FPL's last_rank.
		a.log.Warn("baseline lookup failed, using last_rank", "err", err)
		previous = nil
	}

	rows := standings.Standings.Results
	in := digest.DigestInput{
		LeagueName: standings.League.Name,
		Event:      *ev,
		Standings:  rows,
		Movement:   digest.ComputeMovement(rows, previous),
		Location:   a.cfg.Location,
	}
	if previous != nil {
		if since, err := time.Parse(time.RFC3339, previous.TakenAt); err == nil {
			in.NewEntries = digest.JoinedSince(standings.NewEntries.Results, since)
		}
	}

	if err := a.send(ctx, store.KindDigest, ev.ID, digest.RenderDigest(in)); err != nil {
		return false, err
	}

	// Snapshot only after a successful send, so a failed week does not become
	// the baseline for the next one.
	if err := a.store.PutSnapshot(ctx, digest.ToSnapshot(ev.ID, rows, now)); err != nil {
		a.log.Error("digest sent but snapshot failed", "gw", ev.ID, "err", err)
		return true, err
	}

	a.log.Info("digest sent", "gw", ev.ID, "managers", len(rows), "baseline", in.Movement.Baseline)
	return true, nil
}

// send releases the claim on failure so the next tick retries rather than the
// message being silently dropped.
func (a *App) send(ctx context.Context, kind store.MessageKind, gw int, msg digest.Message) error {
	if err := a.sender.Send(ctx, msg); err != nil {
		if rErr := a.store.Release(ctx, kind, gw); rErr != nil {
			return errors.Join(err, fmt.Errorf("releasing claim: %w", rErr))
		}
		return err
	}
	return nil
}

// nextDeadline prefers FPL's own is_next flag and falls back to the earliest
// deadline still in the future, which is what happens between seasons.
func nextDeadline(events []fpl.Event, now time.Time) *fpl.Event {
	for i := range events {
		if events[i].Next() {
			return &events[i]
		}
	}
	var best *fpl.Event
	for i := range events {
		d, err := events[i].Deadline()
		if err != nil || !d.After(now) {
			continue
		}
		if best == nil {
			best = &events[i]
			continue
		}
		if bd, err := best.Deadline(); err == nil && d.Before(bd) {
			best = &events[i]
		}
	}
	return best
}

// latestChecked returns the most recent gameweek with bonus points applied.
// If an earlier gameweek was never sent (a long outage), it is skipped rather
// than mailing a stale table.
func latestChecked(events []fpl.Event) *fpl.Event {
	var best *fpl.Event
	for i := range events {
		if events[i].Checked() && (best == nil || events[i].ID > best.ID) {
			best = &events[i]
		}
	}
	return best
}

func managerCount(s *fpl.StandingsResponse) int {
	if n := len(s.Standings.Results); n > 0 {
		return n
	}
	return len(s.NewEntries.Results)
}

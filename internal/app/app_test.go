package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"fplbot/internal/config"
	"fplbot/internal/digest"
	"fplbot/internal/fpl"
	"fplbot/internal/store"
)

func ptr[T any](v T) *T { return &v }

// --- fakes ---

type fakeFPL struct {
	events    []fpl.Event
	standings *fpl.StandingsResponse
	err       error
}

func (f *fakeFPL) Events(context.Context) ([]fpl.Event, error) { return f.events, f.err }
func (f *fakeFPL) Standings(context.Context, int) (*fpl.StandingsResponse, error) {
	return f.standings, f.err
}

type fakeStore struct {
	claimed   map[string]bool
	released  []string
	snapshots []store.Snapshot
	baseline  *store.Snapshot
}

func newFakeStore() *fakeStore { return &fakeStore{claimed: map[string]bool{}} }

func claimKey(kind store.MessageKind, gw int) string { return fmt.Sprintf("%s:%d", kind, gw) }

func (f *fakeStore) Claim(_ context.Context, kind store.MessageKind, gw int) (bool, error) {
	k := claimKey(kind, gw)
	if f.claimed[k] {
		return false, nil
	}
	f.claimed[k] = true
	return true, nil
}

func (f *fakeStore) Release(_ context.Context, kind store.MessageKind, gw int) error {
	k := claimKey(kind, gw)
	delete(f.claimed, k)
	f.released = append(f.released, k)
	return nil
}

func (f *fakeStore) PutSnapshot(_ context.Context, snap store.Snapshot) error {
	f.snapshots = append(f.snapshots, snap)
	return nil
}

func (f *fakeStore) LatestSnapshotBefore(context.Context, int) (*store.Snapshot, error) {
	return f.baseline, nil
}

type fakeSender struct {
	sent []digest.Message
	err  error
}

func (f *fakeSender) Send(_ context.Context, m digest.Message) error {
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, m)
	return nil
}

// --- helpers ---

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	loc, err := time.LoadLocation("Europe/London")
	if err != nil {
		t.Fatal(err)
	}
	return &config.Config{
		TableName: "t", LeagueID: 1058423,
		ReminderLead: 24 * time.Hour, Location: loc,
	}
}

func ev(id int, name, deadline string, next, checked bool) fpl.Event {
	return fpl.Event{
		ID: id, Name: name, DeadlineTime: deadline,
		Finished: ptr(checked), DataChecked: ptr(checked),
		IsPrevious: ptr(false), IsCurrent: ptr(false), IsNext: ptr(next),
		AverageEntryScore: 50,
	}
}

func standingsWith(rows []fpl.Standing, newEntries []fpl.NewEntry) *fpl.StandingsResponse {
	res := &fpl.StandingsResponse{League: fpl.League{ID: 1058423, Name: "Leeds Legends Tennis Club"}}
	res.Standings.Results = rows
	res.NewEntries.Results = newEntries
	return res
}

func row(entry int, name string, rank, lastRank, total, eventTotal int) fpl.Standing {
	return fpl.Standing{
		Entry: entry, EntryName: &name, Rank: rank,
		LastRank: lastRank, Total: total, EventTotal: eventTotal,
	}
}

func newApp(t *testing.T, client FPLClient, st Store, sender *fakeSender) *App {
	t.Helper()
	return New(testConfig(t), client, st, sender, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// --- tests ---

func TestReminderFiresInsideLeadWindow(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 30, 0, 0, time.UTC) // 5h before deadline
	client := &fakeFPL{
		events:    []fpl.Event{ev(1, "Gameweek 1", "2026-08-21T17:30:00Z", true, false)},
		standings: standingsWith(nil, []fpl.NewEntry{{EntryName: ptr("A")}}),
	}
	sender := &fakeSender{}
	st := newFakeStore()

	res, err := newApp(t, client, st, sender).Tick(context.Background(), now)

	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if !res.ReminderSent {
		t.Error("ReminderSent = false, want true")
	}
	if len(sender.sent) == 0 {
		t.Fatal("no message sent")
	}
}

func TestReminderSilentOutsideLeadWindow(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC) // 3 days out
	client := &fakeFPL{
		events:    []fpl.Event{ev(1, "Gameweek 1", "2026-08-21T17:30:00Z", true, false)},
		standings: standingsWith(nil, nil),
	}
	sender := &fakeSender{}

	res, err := newApp(t, client, newFakeStore(), sender).Tick(context.Background(), now)

	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.ReminderSent {
		t.Error("ReminderSent = true, want false")
	}
}

// Repeat ticks, scheduler retries and manual invokes all re-evaluate the same
// reminder. Only one send may come of it, whatever the cadence.
func TestTickIsIdempotent(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 30, 0, 0, time.UTC)
	client := &fakeFPL{
		events:    []fpl.Event{ev(1, "Gameweek 1", "2026-08-21T17:30:00Z", true, false)},
		standings: standingsWith(nil, []fpl.NewEntry{{EntryName: ptr("A")}}),
	}
	sender := &fakeSender{}
	st := newFakeStore()
	app := newApp(t, client, st, sender)

	for i := 0; i < 5; i++ {
		if _, err := app.Tick(context.Background(), now.Add(time.Duration(i)*time.Hour)); err != nil {
			t.Fatalf("tick %d: %v", i, err)
		}
	}

	// One reminder, not five.
	if len(sender.sent) != 1 {
		t.Fatalf("sent %d messages, want 1:\n%+v", len(sender.sent), sender.sent)
	}
}

// ReminderLead is deliberately wider than the tick interval, so the window
// always contains a tick and a reminder is late-but-never-missed whatever time
// of day the deadline lands on. The lead it goes out with is then between
// (lead - interval) and lead. This is the invariant the schedule rests on —
// see "Why twice daily" in README.md.
func TestScheduledTicksNeverMissAReminder(t *testing.T) {
	const lead = 48 * time.Hour

	schedules := []struct {
		name     string
		interval time.Duration
	}{
		{"twice daily", 12 * time.Hour}, // the deployed schedule: 09:00 and 21:00
		{"once daily", 24 * time.Hour},
	}

	// FPL deadlines land at a handful of different times of day. 09:00 is the
	// boundary case: it coincides with a tick, giving the full lead.
	clocks := []string{"09:00", "11:00", "12:00", "13:30", "15:00", "17:30", "18:30"}

	for _, sch := range schedules {
		for _, clock := range clocks {
			t.Run(sch.name+" "+clock, func(t *testing.T) {
				deadline, err := time.Parse(time.RFC3339, "2026-08-21T"+clock+":00Z")
				if err != nil {
					t.Fatal(err)
				}

				cfg := testConfig(t)
				cfg.ReminderLead = lead
				client := &fakeFPL{
					events:    []fpl.Event{ev(1, "Gameweek 1", deadline.Format(time.RFC3339), true, false)},
					standings: standingsWith(nil, []fpl.NewEntry{{EntryName: ptr("A")}}),
				}
				sender := &fakeSender{}
				app := New(cfg, client, newFakeStore(), sender, slog.New(slog.NewTextHandler(io.Discard, nil)))

				// Tick on the real cadence, from well outside the window until
				// the deadline has passed.
				var sentLead time.Duration
				start := time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)
				for now := start; !now.After(deadline.Add(sch.interval)); now = now.Add(sch.interval) {
					before := len(sender.sent)
					if _, err := app.Tick(context.Background(), now); err != nil {
						t.Fatalf("tick at %s: %v", now, err)
					}
					if len(sender.sent) > before {
						sentLead = deadline.Sub(now)
					}
				}

				if len(sender.sent) != 1 {
					t.Fatalf("sent %d reminders, want exactly 1", len(sender.sent))
				}
				if sentLead < lead-sch.interval || sentLead > lead {
					t.Errorf("lead = %v, want between %v and %v", sentLead, lead-sch.interval, lead)
				}
			})
		}
	}
}

// Pre-season the table is empty and everyone sits in new_entries. There is
// nothing to report, so nothing should be sent.
func TestNoDigestBeforeLeagueStarts(t *testing.T) {
	now := time.Date(2026, 8, 19, 9, 0, 0, 0, time.UTC) // outside reminder window
	client := &fakeFPL{
		events: []fpl.Event{ev(1, "Gameweek 1", "2026-08-21T17:30:00Z", true, false)},
		standings: standingsWith(nil, []fpl.NewEntry{
			{EntryName: ptr("Dipesh's Team")}, {EntryName: ptr("The Bees")},
		}),
	}
	sender := &fakeSender{}

	res, err := newApp(t, client, newFakeStore(), sender).Tick(context.Background(), now)

	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.DigestSent {
		t.Error("DigestSent = true, want false before any gameweek is scored")
	}
	if len(sender.sent) != 0 {
		t.Errorf("sent %d messages, want none:\n%+v", len(sender.sent), sender.sent)
	}
}

func TestDigestWaitsForDataChecked(t *testing.T) {
	now := time.Date(2026, 8, 24, 9, 0, 0, 0, time.UTC)
	rows := []fpl.Standing{row(1, "A", 1, 0, 70, 70)}
	client := &fakeFPL{
		// GW1 finished but bonus points not yet applied.
		events:    []fpl.Event{ev(1, "Gameweek 1", "2026-08-21T17:30:00Z", false, false)},
		standings: standingsWith(rows, nil),
	}
	sender := &fakeSender{}

	res, err := newApp(t, client, newFakeStore(), sender).Tick(context.Background(), now)

	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.DigestSent {
		t.Error("DigestSent = true, want false until data_checked")
	}
}

func TestDigestSendsAndSnapshots(t *testing.T) {
	now := time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC)
	rows := []fpl.Standing{row(1, "A", 1, 2, 140, 70), row(2, "B", 2, 1, 130, 55)}
	client := &fakeFPL{
		events: []fpl.Event{
			ev(1, "Gameweek 1", "2026-08-21T17:30:00Z", false, true),
			ev(2, "Gameweek 2", "2026-08-28T17:30:00Z", true, false),
		},
		standings: standingsWith(rows, nil),
	}
	sender := &fakeSender{}
	st := newFakeStore()

	res, err := newApp(t, client, st, sender).Tick(context.Background(), now)

	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if !res.DigestSent {
		t.Fatal("DigestSent = false, want true")
	}
	if len(st.snapshots) != 1 || st.snapshots[0].GW != 1 {
		t.Fatalf("snapshots = %+v, want one for GW1", st.snapshots)
	}
	if !strings.Contains(sender.sent[0].Body, "*Table*") {
		t.Errorf("digest body:\n%s", sender.sent[0].Body)
	}
}

// A failed send must not leave the claim in place, or the message is lost.
func TestFailedSendReleasesClaimAndSkipsSnapshot(t *testing.T) {
	now := time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC)
	rows := []fpl.Standing{row(1, "A", 1, 2, 140, 70)}
	client := &fakeFPL{
		events:    []fpl.Event{ev(1, "Gameweek 1", "2026-08-21T17:30:00Z", false, true)},
		standings: standingsWith(rows, nil),
	}
	sender := &fakeSender{err: errors.New("webhook is down")}
	st := newFakeStore()

	res, err := newApp(t, client, st, sender).Tick(context.Background(), now)

	if err == nil {
		t.Fatal("Tick error = nil, want the send failure surfaced")
	}
	if res.DigestSent {
		t.Error("DigestSent = true after a failed send")
	}
	if len(st.released) == 0 {
		t.Error("claim was not released, the digest would never retry")
	}
	if len(st.snapshots) != 0 {
		t.Error("snapshot written despite failed send")
	}
}

func TestFetchFailureIsSurfaced(t *testing.T) {
	client := &fakeFPL{err: errors.New("upstream 503")}

	_, err := newApp(t, client, newFakeStore(), &fakeSender{}).
		Tick(context.Background(), time.Now())

	if err == nil {
		t.Fatal("Tick error = nil, want the fetch failure")
	}
}

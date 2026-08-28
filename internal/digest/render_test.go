package digest

import (
	"strings"
	"testing"
	"time"

	"fplbot/internal/fpl"
)

func london(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/London")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	return loc
}

func ptr[T any](v T) *T { return &v }

func event(id int, name, deadline string) fpl.Event {
	return fpl.Event{
		ID: id, Name: name, DeadlineTime: deadline,
		Finished: ptr(false), DataChecked: ptr(false),
		IsPrevious: ptr(false), IsCurrent: ptr(false), IsNext: ptr(true),
		AverageEntryScore: 51,
	}
}

func mustContain(t *testing.T, body string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Errorf("body missing %q\n---\n%s", w, body)
		}
	}
}

func TestRenderDigest(t *testing.T) {
	ev := event(3, "Gameweek 3", "2026-09-04T17:30:00Z")
	ev.HighestScore = ptr(112)

	rows := []fpl.Standing{
		standing(1, "Three-o Walcott", 1, 2, 200, 70),
		standing(2, "The Bees", 2, 1, 195, 40),
	}

	msg := RenderDigest(DigestInput{
		LeagueName: "Leeds Legends Tennis Club",
		Event:      ev,
		Standings:  rows,
		Movement:   ComputeMovement(rows, nil),
		Location:   london(t),
	})

	mustContain(t, msg.Body,
		"*Leeds Legends Tennis Club* — Gameweek 3",
		"*Table*",
		"1. Three-o Walcott — 200 _(+70)_",
		"*Climbers*",
		"▲1  Three-o Walcott (2→1)",
		"*Fallers*",
		"▼1  The Bees (1→2)",
		"*Gameweek winner*",
		"Three-o Walcott — 70 pts",
		"*Wooden spoon 🥄*",
		"The Bees — 40 pts",
		"Average 51",
		"overall best 112",
	)
	if !strings.Contains(msg.Subject, "Gameweek 3 results") {
		t.Errorf("subject = %q", msg.Subject)
	}
}

func TestRenderDigestOmitsEmptySections(t *testing.T) {
	rows := []fpl.Standing{standing(1, "Static", 1, 1, 100, 50)}

	msg := RenderDigest(DigestInput{
		LeagueName: "L", Event: event(2, "Gameweek 2", "2026-08-28T17:30:00Z"),
		Standings: rows, Movement: ComputeMovement(rows, nil), Location: london(t),
	})

	if strings.Contains(msg.Body, "*Climbers*") || strings.Contains(msg.Body, "*Fallers*") {
		t.Errorf("expected no movement sections\n---\n%s", msg.Body)
	}
	if strings.Contains(msg.Body, "*Just joined*") {
		t.Error("expected no joiners section")
	}
	if strings.Contains(msg.Body, "Wooden spoon") {
		t.Errorf("expected no spoon in a one-manager league\n---\n%s", msg.Body)
	}
}

func TestRenderDigestNamesBaselineGameweek(t *testing.T) {
	rows := []fpl.Standing{standing(1, "A", 1, 1, 100, 50)}
	m := ComputeMovement(rows, nil)
	m.Baseline, m.BaselineGW = BaselineSnapshot, 2

	msg := RenderDigest(DigestInput{
		LeagueName: "L", Event: event(4, "Gameweek 4", "2026-09-11T17:30:00Z"),
		Standings: rows, Movement: m, Location: london(t),
	})

	mustContain(t, msg.Body, "movement vs GW2")
}

func TestRenderReminder(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 30, 0, 0, time.UTC)

	msg := RenderReminder(ReminderInput{
		LeagueName:   "Leeds Legends Tennis Club",
		Event:        event(1, "Gameweek 1", "2026-08-21T17:30:00Z"),
		ManagerCount: 21,
		Now:          now,
		Location:     london(t),
	})

	mustContain(t, msg.Body,
		"⏰ *FPL deadline — Gameweek 1*",
		"Friday 21 Aug, 18:30",
		"5 hours away",
		"21 managers in Leeds Legends Tennis Club.",
		myTeamURL,
	)
}

func TestHumanDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{50 * time.Hour, "2 days"},
		{24 * time.Hour, "1 day"},
		{5 * time.Hour, "5 hours"},
		{time.Hour, "1 hour"},
		{30 * time.Minute, "30 minutes"},
		{time.Minute, "1 minute"},
		{-time.Hour, "0 minutes"},
	}

	for _, tc := range tests {
		if got := humanDuration(tc.d); got != tc.want {
			t.Errorf("humanDuration(%s) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

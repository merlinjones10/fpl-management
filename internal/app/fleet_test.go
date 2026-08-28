package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"fplbot/internal/config"
	"fplbot/internal/fpl"
)

// fleetFPL counts calendar fetches and serves standings per league, so a test
// can tell a shared fetch from a repeated one.
type fleetFPL struct {
	events     []fpl.Event
	eventCalls int
	eventsErr  error
	standings  map[int]*fpl.StandingsResponse
}

func (f *fleetFPL) Events(context.Context) ([]fpl.Event, error) {
	f.eventCalls++
	return f.events, f.eventsErr
}

func (f *fleetFPL) Standings(_ context.Context, leagueID int) (*fpl.StandingsResponse, error) {
	s, ok := f.standings[leagueID]
	if !ok {
		return nil, errors.New("no such league")
	}
	return s, nil
}

func leagueStandings(id int, name string, rows []fpl.Standing) *fpl.StandingsResponse {
	res := &fpl.StandingsResponse{League: fpl.League{ID: id, Name: name}}
	res.Standings.Results = rows
	return res
}

// scoredGW1 is a calendar with GW1 played and bonus applied, so the digest is
// due for any league with a table.
func scoredGW1() []fpl.Event {
	return []fpl.Event{ev(1, "Gameweek 1", "2026-08-21T17:30:00Z", false, true)}
}

var digestDue = time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC)

// The whole argument for one Lambda over a stack per league: the ~1.6MB
// calendar is the same bytes for everyone, so it is fetched once however many
// leagues are running.
func TestFleetFetchesTheCalendarOnceForEveryLeague(t *testing.T) {
	client := &fleetFPL{
		events: scoredGW1(),
		standings: map[int]*fpl.StandingsResponse{
			1: leagueStandings(1, "A", []fpl.Standing{row(11, "A1", 1, 1, 70, 70)}),
			2: leagueStandings(2, "B", []fpl.Standing{row(21, "B1", 1, 1, 60, 60)}),
			3: leagueStandings(3, "C", []fpl.Standing{row(31, "C1", 1, 1, 50, 50)}),
		},
	}

	var apps []*App
	senders := map[int]*fakeSender{}
	for _, id := range []int{1, 2, 3} {
		senders[id] = &fakeSender{}
		apps = append(apps, New(testConfig(t), config.League{ID: id, Channel: config.ChannelLog},
			client, newFakeStore(), senders[id], testLog()))
	}

	res, err := NewFleet(client, apps, testLog()).Tick(context.Background(), digestDue)

	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if client.eventCalls != 1 {
		t.Errorf("Events called %d times, want exactly 1", client.eventCalls)
	}
	if len(res.Leagues) != 3 {
		t.Fatalf("results for %d leagues, want 3", len(res.Leagues))
	}
	for id, s := range senders {
		if len(s.sent) != 1 {
			t.Errorf("league %d sent %d messages, want 1", id, len(s.sent))
		}
	}
}

// Each league renders its own table and reaches its own sender — two leagues
// sharing a webhook would still be two distinct messages.
func TestFleetGivesEachLeagueItsOwnMessage(t *testing.T) {
	client := &fleetFPL{
		events: scoredGW1(),
		standings: map[int]*fpl.StandingsResponse{
			1: leagueStandings(1, "Leeds Legends", []fpl.Standing{row(11, "A1", 1, 1, 70, 70)}),
			2: leagueStandings(2, "Office League", []fpl.Standing{row(21, "B1", 1, 1, 60, 60)}),
		},
	}
	one, two := &fakeSender{}, &fakeSender{}

	apps := []*App{
		New(testConfig(t), config.League{ID: 1, Channel: config.ChannelLog},
			client, newFakeStore(), one, testLog()),
		New(testConfig(t), config.League{ID: 2, Channel: config.ChannelLog},
			client, newFakeStore(), two, testLog()),
	}

	if _, err := NewFleet(client, apps, testLog()).Tick(context.Background(), digestDue); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	if len(one.sent) != 1 || len(two.sent) != 1 {
		t.Fatalf("sent %d and %d messages, want 1 each", len(one.sent), len(two.sent))
	}
	if !strings.Contains(one.sent[0].Body, "Leeds Legends") {
		t.Errorf("league 1 body does not name its league:\n%s", one.sent[0].Body)
	}
	if !strings.Contains(two.sent[0].Body, "Office League") {
		t.Errorf("league 2 body does not name its league:\n%s", two.sent[0].Body)
	}
}

// One broken webhook must not cost the other leagues their messages — the same
// reason the reminder is not short-circuited by a digest failure.
func TestFleetRunsEveryLeagueWhenOneFails(t *testing.T) {
	client := &fleetFPL{
		events: scoredGW1(),
		standings: map[int]*fpl.StandingsResponse{
			1: leagueStandings(1, "A", []fpl.Standing{row(11, "A1", 1, 1, 70, 70)}),
			2: leagueStandings(2, "B", []fpl.Standing{row(21, "B1", 1, 1, 60, 60)}),
			3: leagueStandings(3, "C", []fpl.Standing{row(31, "C1", 1, 1, 50, 50)}),
		},
	}
	broken := &fakeSender{err: errors.New("webhook is down")}
	first, last := &fakeSender{}, &fakeSender{}

	apps := []*App{
		New(testConfig(t), config.League{ID: 1, Channel: config.ChannelLog},
			client, newFakeStore(), first, testLog()),
		New(testConfig(t), config.League{ID: 2, Channel: config.ChannelLog},
			client, newFakeStore(), broken, testLog()),
		New(testConfig(t), config.League{ID: 3, Channel: config.ChannelLog},
			client, newFakeStore(), last, testLog()),
	}

	res, err := NewFleet(client, apps, testLog()).Tick(context.Background(), digestDue)

	if err == nil {
		t.Fatal("Tick error = nil, want league 2's failure surfaced")
	}
	// Attribution matters: one alarm covers every league, so the error text is
	// the only thing that says which one broke.
	if !strings.Contains(err.Error(), "league 2") {
		t.Errorf("error = %v, want it to name league 2", err)
	}
	if len(first.sent) != 1 || len(last.sent) != 1 {
		t.Errorf("healthy leagues sent %d and %d messages, want 1 each",
			len(first.sent), len(last.sent))
	}
	if len(res.Leagues) != 3 {
		t.Errorf("results for %d leagues, want all 3 reported", len(res.Leagues))
	}
	for _, r := range res.Leagues {
		if want := r.LeagueID != 2; r.DigestSent != want {
			t.Errorf("league %d DigestSent = %v, want %v", r.LeagueID, r.DigestSent, want)
		}
	}
}

// Nothing can be decided without the calendar, so this is the one failure that
// stops the tick before any league runs.
func TestFleetCalendarFailureStopsEveryLeague(t *testing.T) {
	client := &fleetFPL{eventsErr: errors.New("upstream 503")}
	sender := &fakeSender{}
	app := New(testConfig(t), config.League{ID: 1, Channel: config.ChannelLog},
		client, newFakeStore(), sender, testLog())

	_, err := NewFleet(client, []*App{app}, testLog()).Tick(context.Background(), digestDue)

	if err == nil {
		t.Fatal("Tick error = nil, want the calendar fetch failure")
	}
	if len(sender.sent) != 0 {
		t.Errorf("sent %d messages despite no calendar", len(sender.sent))
	}
}

package fpl

import (
	"encoding/json"
	"os"
	"testing"
)

func decodeFixture[T any](t *testing.T, name string, out *T) {
	t.Helper()
	raw, err := os.ReadFile("../../testdata/" + name)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
}

// The live pre-season response: standings.results is empty and all 21 managers
// sit in new_entries. This is the state the app sees before GW1 is scored.
func TestPreseasonStandingsFixture(t *testing.T) {
	var res StandingsResponse
	decodeFixture(t, "standings-preseason.json", &res)

	if err := res.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if got, want := res.League.Name, "Leeds Legends Tennis Club"; got != want {
		t.Errorf("league name = %q, want %q", got, want)
	}
	if res.Started() {
		t.Error("Started() = true, want false pre-season")
	}
	if got, want := len(res.NewEntries.Results), 21; got != want {
		t.Errorf("new entries = %d, want %d", got, want)
	}
	if res.LastUpdatedData == nil {
		t.Error("last_updated_data missing")
	}
}

func TestNewEntryDisplayName(t *testing.T) {
	name := "Dipesh's Team"
	first, last := "Dipesh", "Damji"

	tests := []struct {
		name  string
		entry NewEntry
		want  string
	}{
		{"team name wins", NewEntry{EntryName: &name, PlayerFirstName: &first}, "Dipesh's Team"},
		{"falls back to manager", NewEntry{PlayerFirstName: &first, PlayerLastName: &last}, "Dipesh Damji"},
		{"first name only", NewEntry{PlayerFirstName: &first}, "Dipesh"},
		{"nothing usable", NewEntry{}, "Unknown team"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.entry.DisplayName(); got != tc.want {
				t.Errorf("DisplayName() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEventsFixture(t *testing.T) {
	var b Bootstrap
	decodeFixture(t, "bootstrap-events.json", &b)

	if err := b.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if got, want := len(b.Events), 38; got != want {
		t.Errorf("events = %d, want %d", got, want)
	}

	gw1 := b.Events[0]
	if !gw1.Next() {
		t.Error("GW1 should be is_next pre-season")
	}
	if gw1.Checked() {
		t.Error("GW1 should not be data_checked pre-season")
	}
	if _, err := gw1.Deadline(); err != nil {
		t.Errorf("parse deadline: %v", err)
	}
	// highest_score is null until a gameweek is scored.
	if gw1.HighestScore != nil {
		t.Errorf("highest_score = %v, want nil", *gw1.HighestScore)
	}
}

// A field disappearing must fail loudly rather than decoding to false and
// silently changing a decision.
func TestEventValidateRejectsMissingFlags(t *testing.T) {
	var e Event
	if err := json.Unmarshal([]byte(`{"id":5,"deadline_time":"2026-09-04T17:30:00Z"}`), &e); err != nil {
		t.Fatal(err)
	}
	if err := e.Validate(); err == nil {
		t.Fatal("Validate() = nil, want error for missing data_checked")
	}
}

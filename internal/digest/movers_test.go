package digest

import (
	"testing"
	"time"

	"fplbot/internal/fpl"
	"fplbot/internal/store"
)

func standing(entry int, name string, rank, lastRank, total, eventTotal int) fpl.Standing {
	return fpl.Standing{
		Entry: entry, EntryName: &name, Rank: rank,
		LastRank: lastRank, Total: total, EventTotal: eventTotal,
	}
}

func TestComputeMovementUsesSnapshotBaseline(t *testing.T) {
	rows := []fpl.Standing{
		standing(1, "Three-o Walcott", 1, 3, 200, 70),
		standing(2, "The Bees", 2, 1, 195, 40),
		standing(3, "Club Aqua", 3, 2, 190, 50),
	}
	// Baseline disagrees with last_rank on purpose: the snapshot must win.
	prev := &store.Snapshot{GW: 3, Rows: []store.SnapshotRow{
		{Entry: 1, Rank: 9}, {Entry: 2, Rank: 2}, {Entry: 3, Rank: 1},
	}}

	m := ComputeMovement(rows, prev)

	if m.Baseline != BaselineSnapshot {
		t.Errorf("baseline = %q, want %q", m.Baseline, BaselineSnapshot)
	}
	if m.BaselineGW != 3 {
		t.Errorf("baselineGW = %d, want 3", m.BaselineGW)
	}
	if len(m.Climbers) != 1 || m.Climbers[0].Entry != 1 || m.Climbers[0].Delta != 8 {
		t.Fatalf("climbers = %+v, want entry 1 up 8", m.Climbers)
	}
	if len(m.Fallers) != 1 || m.Fallers[0].Entry != 3 || m.Fallers[0].Delta != -2 {
		t.Fatalf("fallers = %+v, want entry 3 down 2", m.Fallers)
	}
}

func TestComputeMovementFallsBackToLastRank(t *testing.T) {
	rows := []fpl.Standing{standing(1, "Club Aqua", 7, 12, 150, 60)}

	m := ComputeMovement(rows, nil)

	if m.Baseline != BaselineAPI {
		t.Errorf("baseline = %q, want %q", m.Baseline, BaselineAPI)
	}
	if len(m.Climbers) != 1 || m.Climbers[0].Delta != 5 {
		t.Fatalf("climbers = %+v, want one climber up 5", m.Climbers)
	}
}

// last_rank is 0 at the start of a phase — reporting that as a climb would
// have everyone leaping up the table in GW1.
func TestComputeMovementIgnoresZeroRanks(t *testing.T) {
	rows := []fpl.Standing{
		standing(1, "Fresh Start", 4, 0, 60, 60),
		standing(2, "Unranked", 0, 3, 0, 0),
		standing(3, "Static", 5, 5, 55, 55),
	}

	m := ComputeMovement(rows, nil)

	if len(m.Climbers) != 0 || len(m.Fallers) != 0 {
		t.Fatalf("movement = %+v, want none", m)
	}
}

func TestComputeMovementTreatsLateJoinerAsUnranked(t *testing.T) {
	rows := []fpl.Standing{
		standing(1, "Incumbent", 1, 1, 200, 70),
		standing(99, "Late Joiner", 2, 0, 60, 60),
	}
	prev := &store.Snapshot{GW: 2, Rows: []store.SnapshotRow{{Entry: 1, Rank: 1}}}

	m := ComputeMovement(rows, prev)

	if len(m.Climbers) != 0 || len(m.Fallers) != 0 {
		t.Fatalf("movement = %+v, want none for a manager with no baseline", m)
	}
}

func TestComputeMovementCapsAtTopN(t *testing.T) {
	var rows []fpl.Standing
	var prevRows []store.SnapshotRow
	for i := 1; i <= 12; i++ {
		rows = append(rows, standing(i, "Team", i, 0, 100, 50))
		prevRows = append(prevRows, store.SnapshotRow{Entry: i, Rank: i + i}) // everyone climbs
	}

	m := ComputeMovement(rows, &store.Snapshot{GW: 1, Rows: prevRows})

	if len(m.Climbers) != topN {
		t.Fatalf("climbers = %d, want %d", len(m.Climbers), topN)
	}
	if m.Climbers[0].Delta < m.Climbers[1].Delta {
		t.Error("climbers not sorted by delta descending")
	}
}

func TestGameweekWinnersIncludesTies(t *testing.T) {
	rows := []fpl.Standing{
		standing(1, "A", 1, 1, 200, 88),
		standing(2, "B", 2, 2, 190, 88),
		standing(3, "C", 3, 3, 180, 40),
	}

	w := GameweekWinners(rows)

	if w == nil || w.Points != 88 {
		t.Fatalf("winners = %+v, want 88 points", w)
	}
	if len(w.Names) != 2 {
		t.Errorf("names = %v, want two tied winners", w.Names)
	}
}

func TestGameweekWinnersEmpty(t *testing.T) {
	if got := GameweekWinners(nil); got != nil {
		t.Errorf("GameweekWinners(nil) = %+v, want nil", got)
	}
}

func TestWoodenSpoonIncludesTies(t *testing.T) {
	rows := []fpl.Standing{
		standing(1, "A", 1, 1, 200, 88),
		standing(2, "B", 2, 2, 190, 12),
		standing(3, "C", 3, 3, 180, 12),
	}

	s := WoodenSpoon(rows)

	if s == nil || s.Points != 12 {
		t.Fatalf("spoon = %+v, want 12 points", s)
	}
	if len(s.Names) != 2 {
		t.Errorf("names = %v, want two tied", s.Names)
	}
}

// Nothing to award when the spoon would name everyone — including the winner.
func TestWoodenSpoonSkipsWholeLeague(t *testing.T) {
	tests := map[string][]fpl.Standing{
		"empty": nil,
		"one manager": {
			standing(1, "A", 1, 1, 100, 50),
		},
		"all tied": {
			standing(1, "A", 1, 1, 100, 50),
			standing(2, "B", 2, 2, 100, 50),
		},
	}

	for name, rows := range tests {
		if got := WoodenSpoon(rows); got != nil {
			t.Errorf("%s: WoodenSpoon = %+v, want nil", name, got)
		}
	}
}

func TestJoinedSinceFiltersAndSorts(t *testing.T) {
	mk := func(name, joined string) fpl.NewEntry {
		n := name
		return fpl.NewEntry{EntryName: &n, JoinedTime: joined}
	}
	entries := []fpl.NewEntry{
		mk("old", "2026-08-01T10:00:00Z"),
		mk("newest", "2026-08-20T10:00:00Z"),
		mk("middle", "2026-08-15T10:00:00Z"),
		mk("unparseable", "not a time"),
	}
	cutoff := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)

	got := JoinedSince(entries, cutoff)

	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2", len(got))
	}
	if got[0].DisplayName() != "newest" || got[1].DisplayName() != "middle" {
		t.Errorf("order = %q, %q; want newest, middle", got[0].DisplayName(), got[1].DisplayName())
	}
}

func TestToSnapshot(t *testing.T) {
	rows := []fpl.Standing{standing(7, "Club Aqua", 2, 3, 150, 60)}
	at := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

	snap := ToSnapshot(4, rows, at)

	if snap.GW != 4 || snap.TakenAt != "2026-09-01T12:00:00Z" {
		t.Errorf("snapshot meta = %+v", snap)
	}
	if len(snap.Rows) != 1 || snap.Rows[0].Name != "Club Aqua" || snap.Rows[0].Rank != 2 {
		t.Errorf("snapshot rows = %+v", snap.Rows)
	}
}

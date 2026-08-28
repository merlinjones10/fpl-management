// Package digest turns a league table into the weekly summary: who climbed,
// who fell, who won the gameweek.
package digest

import (
	"sort"
	"time"

	"fplbot/internal/fpl"
	"fplbot/internal/store"
)

const topN = 5

type Mover struct {
	Entry int
	Name  string
	From  int
	To    int
	// Delta is positive when the manager climbed.
	Delta int
}

type Baseline string

const (
	// BaselineSnapshot compares against our own stored table.
	BaselineSnapshot Baseline = "snapshot"
	// BaselineAPI compares against FPL's last_rank, which only ever means
	// "last gameweek" and resets to 0 at the start of a phase.
	BaselineAPI Baseline = "api"
)

type Movement struct {
	Climbers   []Mover
	Fallers    []Mover
	Baseline   Baseline
	BaselineGW int
}

func ComputeMovement(rows []fpl.Standing, previous *store.Snapshot) Movement {
	prevRanks := map[int]int{}
	m := Movement{Baseline: BaselineAPI}

	if previous != nil {
		m.Baseline = BaselineSnapshot
		m.BaselineGW = previous.GW
		for _, r := range previous.Rows {
			prevRanks[r.Entry] = r.Rank
		}
	}

	moves := make([]Mover, 0, len(rows))
	for _, row := range rows {
		from, ok := prevRanks[row.Entry]
		if !ok {
			from = row.LastRank
		}

		// Rank 0 means "no rank yet": the start of a phase, or a manager who
		// joined after the baseline was taken. No movement to report either way.
		if from <= 0 || row.Rank <= 0 || from == row.Rank {
			continue
		}

		moves = append(moves, Mover{
			Entry: row.Entry,
			Name:  row.DisplayName(),
			From:  from,
			To:    row.Rank,
			Delta: from - row.Rank,
		})
	}

	climbers := filter(moves, func(m Mover) bool { return m.Delta > 0 })
	sort.Slice(climbers, func(i, j int) bool {
		if climbers[i].Delta != climbers[j].Delta {
			return climbers[i].Delta > climbers[j].Delta
		}
		return climbers[i].To < climbers[j].To
	})

	fallers := filter(moves, func(m Mover) bool { return m.Delta < 0 })
	sort.Slice(fallers, func(i, j int) bool {
		if fallers[i].Delta != fallers[j].Delta {
			return fallers[i].Delta < fallers[j].Delta
		}
		return fallers[i].To < fallers[j].To
	})

	m.Climbers = truncate(climbers, topN)
	m.Fallers = truncate(fallers, topN)
	return m
}

// Scorers is one gameweek score and every manager who posted it.
type Scorers struct {
	Names  []string
	Points int
}

// GameweekWinners returns the highest single-gameweek score, ties included.
func GameweekWinners(rows []fpl.Standing) *Scorers {
	return extreme(rows, func(score, best int) bool { return score > best })
}

// WoodenSpoon returns the lowest single-gameweek score, ties included. It is
// nil when naming it would name the whole league — a one-manager table, or a
// week where everybody scored the same and the spoon is also the winner.
func WoodenSpoon(rows []fpl.Standing) *Scorers {
	s := extreme(rows, func(score, worst int) bool { return score < worst })
	if s == nil || len(s.Names) == len(rows) {
		return nil
	}
	return s
}

func extreme(rows []fpl.Standing, beats func(score, incumbent int) bool) *Scorers {
	if len(rows) == 0 {
		return nil
	}
	best := rows[0].EventTotal
	for _, r := range rows[1:] {
		if beats(r.EventTotal, best) {
			best = r.EventTotal
		}
	}
	s := &Scorers{Points: best}
	for _, r := range rows {
		if r.EventTotal == best {
			s.Names = append(s.Names, r.DisplayName())
		}
	}
	return s
}

func ToSnapshot(gw int, rows []fpl.Standing, takenAt time.Time) store.Snapshot {
	snap := store.Snapshot{GW: gw, TakenAt: takenAt.UTC().Format(time.RFC3339), Rows: make([]store.SnapshotRow, 0, len(rows))}
	for _, r := range rows {
		snap.Rows = append(snap.Rows, store.SnapshotRow{
			Entry: r.Entry, Name: r.DisplayName(), Rank: r.Rank, Total: r.Total,
		})
	}
	return snap
}

// JoinedSince returns managers who joined after t, newest first.
func JoinedSince(entries []fpl.NewEntry, t time.Time) []fpl.NewEntry {
	var out []fpl.NewEntry
	for _, e := range entries {
		if joined, ok := e.Joined(); ok && joined.After(t) {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, _ := out[i].Joined()
		b, _ := out[j].Joined()
		return a.After(b)
	})
	return out
}

func filter[T any](in []T, keep func(T) bool) []T {
	out := make([]T, 0, len(in))
	for _, v := range in {
		if keep(v) {
			out = append(out, v)
		}
	}
	return out
}

func truncate[T any](in []T, n int) []T {
	if len(in) > n {
		return in[:n]
	}
	return in
}

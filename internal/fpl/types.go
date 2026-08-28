// Package fpl is a thin read-only client for the unofficial Fantasy Premier
// League API.
//
// Field sets below were derived from live responses captured 2026-08-21
// (pre-GW1, season 2026/27). The API is undocumented and unversioned: it gains
// fields between seasons and nulls things out pre-season. encoding/json ignores
// unknown fields, so new ones are harmless; the risk is the opposite direction,
// where a *removed* field decodes to a zero value and silently changes a
// decision. Flags the app branches on are therefore pointers, and Validate
// rejects a response that omits them.
package fpl

import (
	"fmt"
	"time"
)

type Event struct {
	ID                int    `json:"id"`
	Name              string `json:"name"`
	DeadlineTime      string `json:"deadline_time"`
	DeadlineTimeEpoch int64  `json:"deadline_time_epoch"`
	Finished          *bool  `json:"finished"`
	// DataChecked reports that bonus points are applied and scores verified.
	// The digest waits for this rather than Finished, or it reports pre-bonus totals.
	DataChecked       *bool `json:"data_checked"`
	IsPrevious        *bool `json:"is_previous"`
	IsCurrent         *bool `json:"is_current"`
	IsNext            *bool `json:"is_next"`
	AverageEntryScore int   `json:"average_entry_score"`
	HighestScore      *int  `json:"highest_score"`
}

func (e Event) Deadline() (time.Time, error) {
	return time.Parse(time.RFC3339, e.DeadlineTime)
}

func (e Event) Checked() bool { return e.DataChecked != nil && *e.DataChecked }
func (e Event) Next() bool    { return e.IsNext != nil && *e.IsNext }
func (e Event) Current() bool { return e.IsCurrent != nil && *e.IsCurrent }

func (e Event) Validate() error {
	switch {
	case e.ID == 0:
		return fmt.Errorf("event: missing id")
	case e.DeadlineTime == "":
		return fmt.Errorf("event %d: missing deadline_time", e.ID)
	case e.DataChecked == nil:
		return fmt.Errorf("event %d: missing data_checked", e.ID)
	case e.IsNext == nil || e.IsCurrent == nil:
		return fmt.Errorf("event %d: missing is_next/is_current", e.ID)
	}
	if _, err := e.Deadline(); err != nil {
		return fmt.Errorf("event %d: bad deadline_time %q: %w", e.ID, e.DeadlineTime, err)
	}
	return nil
}

type Bootstrap struct {
	Events []Event `json:"events"`
}

func (b Bootstrap) Validate() error {
	if len(b.Events) == 0 {
		return fmt.Errorf("bootstrap: no events")
	}
	for _, e := range b.Events {
		if err := e.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// NewEntry is a manager who has joined but has no scored gameweek yet.
// Note the shape differs from Standing: the name is split across two fields.
type NewEntry struct {
	Entry           int     `json:"entry"`
	EntryName       *string `json:"entry_name"`
	JoinedTime      string  `json:"joined_time"`
	PlayerFirstName *string `json:"player_first_name"`
	PlayerLastName  *string `json:"player_last_name"`
}

type Standing struct {
	Entry      int     `json:"entry"`
	EntryName  *string `json:"entry_name"`
	PlayerName *string `json:"player_name"`
	Rank       int     `json:"rank"`
	// LastRank is FPL's own previous-gameweek rank. It is 0 at the start of a phase.
	LastRank   int `json:"last_rank"`
	Total      int `json:"total"`
	EventTotal int `json:"event_total"`
}

type League struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	LeagueType string `json:"league_type"`
	Scoring    string `json:"scoring"`
	StartEvent int    `json:"start_event"`
	AdminEntry *int   `json:"admin_entry"`
}

type page[T any] struct {
	HasNext bool `json:"has_next"`
	Page    int  `json:"page"`
	Results []T  `json:"results"`
}

type StandingsResponse struct {
	LastUpdatedData *string        `json:"last_updated_data"`
	League          League         `json:"league"`
	NewEntries      page[NewEntry] `json:"new_entries"`
	Standings       page[Standing] `json:"standings"`
}

func (s StandingsResponse) Validate() error {
	if s.League.ID == 0 {
		return fmt.Errorf("standings: missing league.id")
	}
	if s.League.Name == "" {
		return fmt.Errorf("standings: missing league.name")
	}
	return nil
}

// Started reports whether any gameweek has been scored for this league yet.
// Before that, every manager sits in NewEntries and Standings is empty.
func (s StandingsResponse) Started() bool { return len(s.Standings.Results) > 0 }

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// DisplayName prefers the team name, falling back to the manager's name.
func (s Standing) DisplayName() string {
	if n := deref(s.EntryName); n != "" {
		return n
	}
	if n := deref(s.PlayerName); n != "" {
		return n
	}
	return "Unknown team"
}

func (n NewEntry) DisplayName() string {
	if s := deref(n.EntryName); s != "" {
		return s
	}
	manager := deref(n.PlayerFirstName)
	if last := deref(n.PlayerLastName); last != "" {
		if manager != "" {
			manager += " "
		}
		manager += last
	}
	if manager != "" {
		return manager
	}
	return "Unknown team"
}

func (n NewEntry) Joined() (time.Time, bool) {
	t, err := time.Parse(time.RFC3339, n.JoinedTime)
	return t, err == nil
}

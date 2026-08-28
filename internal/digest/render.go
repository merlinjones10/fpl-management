package digest

import (
	"fmt"
	"math"
	"strings"
	"time"

	"fplbot/internal/fpl"
)

// Messages are plaintext formatted with WhatsApp markup (*bold*, _italic_) so
// the whole body can be copied out of the email and pasted into a group chat
// with no editing. That rules out HTML mail and markdown-only syntax.

type Message struct {
	Subject string
	Body    string
}

const myTeamURL = "https://fantasy.premierleague.com/my-team"

type DigestInput struct {
	LeagueName string
	Event      fpl.Event
	Standings  []fpl.Standing
	NewEntries []fpl.NewEntry
	Movement   Movement
	Location   *time.Location
}

func RenderDigest(in DigestInput) Message {
	var b strings.Builder

	fmt.Fprintf(&b, "*%s* — %s\n\n", in.LeagueName, in.Event.Name)

	b.WriteString("*Table*\n")
	for _, row := range in.Standings {
		fmt.Fprintf(&b, "%d. %s — %d _(+%d)_\n", row.Rank, row.DisplayName(), row.Total, row.EventTotal)
	}

	if len(in.Movement.Climbers) > 0 {
		b.WriteString("\n*Climbers*\n")
		for _, m := range in.Movement.Climbers {
			fmt.Fprintf(&b, "▲%d  %s (%d→%d)\n", m.Delta, m.Name, m.From, m.To)
		}
	}

	if len(in.Movement.Fallers) > 0 {
		b.WriteString("\n*Fallers*\n")
		for _, m := range in.Movement.Fallers {
			fmt.Fprintf(&b, "▼%d  %s (%d→%d)\n", -m.Delta, m.Name, m.From, m.To)
		}
	}

	writeScorers(&b, "Gameweek winner", "Gameweek winners", GameweekWinners(in.Standings))
	writeScorers(&b, "Wooden spoon 🥄", "Wooden spoons 🥄", WoodenSpoon(in.Standings))

	if len(in.NewEntries) > 0 {
		b.WriteString("\n*Just joined*\n")
		for _, e := range in.NewEntries {
			fmt.Fprintf(&b, "%s\n", e.DisplayName())
		}
	}

	b.WriteString("\n" + italic(digestFooter(in)) + "\n")

	return Message{
		Subject: fmt.Sprintf("%s — %s results", in.LeagueName, in.Event.Name),
		Body:    b.String(),
	}
}

func writeScorers(b *strings.Builder, singular, plural string, s *Scorers) {
	if s == nil {
		return
	}
	label := singular
	if len(s.Names) > 1 {
		label = plural
	}
	fmt.Fprintf(b, "\n*%s*\n%s — %d pts\n", label, strings.Join(s.Names, ", "), s.Points)
}

func digestFooter(in DigestInput) string {
	parts := []string{fmt.Sprintf("World average %d", in.Event.AverageEntryScore)}
	if in.Event.HighestScore != nil {
		parts = append(parts, fmt.Sprintf("world best %d", *in.Event.HighestScore))
	}
	switch {
	case in.Movement.Baseline == BaselineSnapshot && in.Movement.BaselineGW > 0:
		parts = append(parts, fmt.Sprintf("movement vs GW%d", in.Movement.BaselineGW))
	default:
		parts = append(parts, "movement vs last gameweek")
	}
	return strings.Join(parts, " · ")
}

type ReminderInput struct {
	LeagueName   string
	Event        fpl.Event
	ManagerCount int
	Now          time.Time
	Location     *time.Location
}

func RenderReminder(in ReminderInput) Message {
	deadline, _ := in.Event.Deadline()
	var b strings.Builder

	fmt.Fprintf(&b, "⏰ *FPL deadline — %s*\n", in.Event.Name)
	fmt.Fprintf(&b, "%s _(%s away)_\n\n", formatWhen(deadline, in.Location), humanDuration(deadline.Sub(in.Now)))

	if in.ManagerCount > 0 {
		fmt.Fprintf(&b, "%d managers in %s.\n\n", in.ManagerCount, in.LeagueName)
	}
	b.WriteString("Sort your team: " + myTeamURL + "\n")

	return Message{
		Subject: fmt.Sprintf("%s deadline %s — %s", in.Event.Name, humanDuration(deadline.Sub(in.Now)), in.LeagueName),
		Body:    b.String(),
	}
}

func italic(s string) string { return "_" + s + "_" }

func formatWhen(t time.Time, loc *time.Location) string {
	if loc == nil {
		loc = time.UTC
	}
	return t.In(loc).Format("Monday 2 Jan, 15:04")
}

func humanDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	// Truncate rather than round: 30 minutes before a deadline must not read
	// as "1 hour away".
	switch {
	case d >= 24*time.Hour:
		return plural(int(d.Hours())/24, "day")
	case d >= time.Hour:
		return plural(int(d.Hours()), "hour")
	default:
		return plural(int(math.Round(d.Minutes())), "minute")
	}
}

func plural(n int, unit string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", unit)
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

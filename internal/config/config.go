package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Channel selects the delivery transport. Adding one means a new notify.Sender
// implementation and a case here — nothing else in the app changes.
type Channel string

const (
	ChannelDiscord Channel = "discord"
	ChannelSlack   Channel = "slack"
	ChannelLog     Channel = "log"
)

// League is one league and where its messages go. Leagues are independent: each
// has its own DynamoDB partition and its own sender, so moving one to another
// transport touches nothing but its own entry.
type League struct {
	ID      int     `json:"id"`
	Channel Channel `json:"channel"`

	// WebhookParam is an SSM parameter name, not the webhook URL itself. A URL
	// embeds its token, so it is fetched at cold start and never sits in the
	// function config, where anyone with lambda:GetFunction could read it.
	// Two leagues may name the same parameter and share a channel.
	WebhookParam string `json:"webhookParam"`
}

type Config struct {
	TableName string
	Leagues   []League

	// ReminderLead is how far ahead of a deadline the reminder fires.
	ReminderLead time.Duration
	Location     *time.Location
	// DryRun prints the message instead of delivering it.
	DryRun bool
}

func Load() (*Config, error) {
	c := &Config{
		TableName: os.Getenv("TABLE_NAME"),
		DryRun:    os.Getenv("DRY_RUN") == "true",
	}
	var errs []string

	if c.TableName == "" {
		errs = append(errs, "TABLE_NAME is required")
	}

	leagues, leagueErrs := parseLeagues(os.Getenv("LEAGUES"))
	c.Leagues = leagues
	errs = append(errs, leagueErrs...)

	hours := 48.0
	if raw := os.Getenv("REMINDER_LEAD_HOURS"); raw != "" {
		h, err := strconv.ParseFloat(raw, 64)
		if err != nil || h <= 0 {
			errs = append(errs, "REMINDER_LEAD_HOURS must be a positive number")
		} else {
			hours = h
		}
	}
	c.ReminderLead = time.Duration(hours * float64(time.Hour))

	tz := os.Getenv("TIMEZONE")
	if tz == "" {
		tz = "Europe/London"
	}
	// Deadlines come back as UTC; rendering them in the wrong zone puts the
	// reminder an hour out for most of the season.
	loc, err := time.LoadLocation(tz)
	if err != nil {
		errs = append(errs, fmt.Sprintf("TIMEZONE %q is not a known location: %v", tz, err))
	}
	c.Location = loc

	if len(errs) > 0 {
		return nil, fmt.Errorf("config: %s", strings.Join(errs, "; "))
	}
	return c, nil
}

// parseLeagues reads LEAGUES, a JSON array of League. Every entry is checked
// and every fault reported, so one bad league does not hide the next.
func parseLeagues(raw string) ([]League, []string) {
	if strings.TrimSpace(raw) == "" {
		return nil, []string{`LEAGUES is required: a JSON array of {"id","channel","webhookParam"}`}
	}

	var leagues []League
	if err := json.Unmarshal([]byte(raw), &leagues); err != nil {
		return nil, []string{fmt.Sprintf("LEAGUES is not valid JSON: %v", err)}
	}
	if len(leagues) == 0 {
		return nil, []string{"LEAGUES is empty, there is nothing to send"}
	}

	var errs []string
	seen := make(map[int]bool, len(leagues))

	for i := range leagues {
		l := &leagues[i]
		where := fmt.Sprintf("LEAGUES[%d]", i)

		switch {
		case l.ID <= 0:
			errs = append(errs, where+": id must be a positive integer")
		case seen[l.ID]:
			// Both entries would claim the same partition: one sends, the other
			// silently loses the conditional write and looks like a quiet week.
			errs = append(errs, fmt.Sprintf("%s: league %d appears twice", where, l.ID))
		}
		seen[l.ID] = true

		l.Channel = Channel(strings.ToLower(strings.TrimSpace(string(l.Channel))))
		if l.Channel == "" {
			l.Channel = ChannelDiscord
		}

		// Only the selected channel's settings are required, so moving a league
		// between transports does not mean carrying config for the one it left.
		switch l.Channel {
		case ChannelDiscord, ChannelSlack:
			if l.WebhookParam == "" {
				errs = append(errs, fmt.Sprintf(
					"%s: webhookParam is required for the %s channel", where, l.Channel))
			}
		case ChannelLog:
			// Nothing to configure.
		default:
			errs = append(errs, fmt.Sprintf(
				"%s: channel %q is not one of discord, slack, log", where, l.Channel))
		}
	}
	return leagues, errs
}

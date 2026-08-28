package config

import (
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
	ChannelLog     Channel = "log"
)

type Config struct {
	TableName string
	LeagueID  int

	Channel Channel

	// DiscordWebhookParam is an SSM parameter name, not the webhook URL itself.
	// The URL embeds the webhook token, so it is fetched at cold start and never
	// sits in the function config, where anyone with lambda:GetFunction could
	// read it.
	DiscordWebhookParam string

	// ReminderLead is how far ahead of a deadline the reminder fires.
	ReminderLead time.Duration
	Location     *time.Location
	// DryRun prints the message instead of delivering it.
	DryRun bool
}

func Load() (*Config, error) {
	c := &Config{
		TableName:           os.Getenv("TABLE_NAME"),
		DiscordWebhookParam: os.Getenv("DISCORD_WEBHOOK_PARAM"),
		DryRun:              os.Getenv("DRY_RUN") == "true",
	}
	var errs []string

	if c.TableName == "" {
		errs = append(errs, "TABLE_NAME is required")
	}

	id, err := strconv.Atoi(os.Getenv("LEAGUE_ID"))
	if err != nil || id <= 0 {
		errs = append(errs, "LEAGUE_ID must be a positive integer")
	}
	c.LeagueID = id

	c.Channel = Channel(strings.ToLower(strings.TrimSpace(os.Getenv("NOTIFY_CHANNEL"))))
	if c.Channel == "" {
		c.Channel = ChannelDiscord
	}

	// Only the selected channel's settings are required, so switching transport
	// does not mean carrying config for the one you are not using.
	switch c.Channel {
	case ChannelDiscord:
		if c.DiscordWebhookParam == "" {
			errs = append(errs, "DISCORD_WEBHOOK_PARAM is required for the discord channel")
		}
	case ChannelLog:
		// Nothing to configure.
	default:
		errs = append(errs, fmt.Sprintf("NOTIFY_CHANNEL %q is not one of discord, log", c.Channel))
	}

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

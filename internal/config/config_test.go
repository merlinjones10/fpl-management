package config

import (
	"strings"
	"testing"
)

func setEnv(t *testing.T, kv map[string]string) {
	t.Helper()
	base := map[string]string{
		"TABLE_NAME": "fpl-state",
		"LEAGUE_ID":  "1058423",
	}
	for k, v := range kv {
		base[k] = v
	}
	// Clear everything the loader reads, so one case cannot leak into the next.
	for _, k := range []string{
		"TABLE_NAME", "LEAGUE_ID", "NOTIFY_CHANNEL", "DISCORD_WEBHOOK_PARAM",
		"REMINDER_LEAD_HOURS", "TIMEZONE", "DRY_RUN",
	} {
		t.Setenv(k, "")
	}
	for k, v := range base {
		t.Setenv(k, v)
	}
}

func TestLoadDefaultsToDiscord(t *testing.T) {
	setEnv(t, map[string]string{"DISCORD_WEBHOOK_PARAM": "/fpl/discord-webhook"})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Channel != ChannelDiscord {
		t.Errorf("Channel = %q, want discord", cfg.Channel)
	}
	if cfg.Location.String() != "Europe/London" {
		t.Errorf("Location = %q", cfg.Location)
	}
	if cfg.ReminderLead.Hours() != 48 {
		t.Errorf("ReminderLead = %v, want 48h", cfg.ReminderLead)
	}
}

func TestLoadRejectsIncompleteChannel(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{
			"discord without webhook param",
			map[string]string{"NOTIFY_CHANNEL": "discord"},
			"DISCORD_WEBHOOK_PARAM",
		},
		{
			"unknown channel",
			map[string]string{"NOTIFY_CHANNEL": "carrier-pigeon"},
			"not one of",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setEnv(t, tc.env)

			_, err := Load()

			if err == nil {
				t.Fatal("Load error = nil, want a validation failure")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want mention of %q", err, tc.want)
			}
		})
	}
}

func TestLoadAcceptsLogChannelWithNoTransportConfig(t *testing.T) {
	setEnv(t, map[string]string{"NOTIFY_CHANNEL": "log"})

	if _, err := Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
}

package config

import (
	"strings"
	"testing"
)

func setEnv(t *testing.T, kv map[string]string) {
	t.Helper()
	base := map[string]string{
		"TABLE_NAME": "fpl-state",
		"LEAGUES":    `[{"id":1058423,"channel":"discord","webhookParam":"/fpl/a-discord"}]`,
	}
	for k, v := range kv {
		base[k] = v
	}
	// Clear everything the loader reads, so one case cannot leak into the next.
	for _, k := range []string{
		"TABLE_NAME", "LEAGUES", "REMINDER_LEAD_HOURS", "TIMEZONE", "DRY_RUN",
	} {
		t.Setenv(k, "")
	}
	for k, v := range base {
		t.Setenv(k, v)
	}
}

func TestLoadDefaults(t *testing.T) {
	setEnv(t, nil)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Leagues) != 1 || cfg.Leagues[0].ID != 1058423 {
		t.Fatalf("Leagues = %+v, want the one league", cfg.Leagues)
	}
	if cfg.Leagues[0].Channel != ChannelDiscord {
		t.Errorf("Channel = %q, want discord", cfg.Leagues[0].Channel)
	}
	if cfg.Location.String() != "Europe/London" {
		t.Errorf("Location = %q", cfg.Location)
	}
	if cfg.ReminderLead.Hours() != 48 {
		t.Errorf("ReminderLead = %v, want 48h", cfg.ReminderLead)
	}
}

// The whole point of the list: leagues are independent, and each carries its
// own transport.
func TestLoadAcceptsSeveralLeaguesOnDifferentChannels(t *testing.T) {
	setEnv(t, map[string]string{"LEAGUES": `[
		{"id":1058423,"channel":"discord","webhookParam":"/fpl/shared-discord"},
		{"id":2222222,"channel":"discord","webhookParam":"/fpl/shared-discord"},
		{"id":3333333,"channel":"slack","webhookParam":"/fpl/c-slack"}
	]`})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Leagues) != 3 {
		t.Fatalf("loaded %d leagues, want 3", len(cfg.Leagues))
	}
	// Two leagues sharing one webhook is legitimate: it is how a new league
	// runs into the existing channel until its own is ready.
	if cfg.Leagues[0].WebhookParam != cfg.Leagues[1].WebhookParam {
		t.Error("leagues that name the same parameter should keep it")
	}
	if cfg.Leagues[2].Channel != ChannelSlack {
		t.Errorf("Channel = %q, want slack", cfg.Leagues[2].Channel)
	}
}

// A channel is defaulted per league, not globally.
func TestLoadDefaultsEachLeagueToDiscord(t *testing.T) {
	setEnv(t, map[string]string{"LEAGUES": `[
		{"id":1,"webhookParam":"/fpl/a"},
		{"id":2,"channel":"log"}
	]`})

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Leagues[0].Channel != ChannelDiscord {
		t.Errorf("Leagues[0].Channel = %q, want the discord default", cfg.Leagues[0].Channel)
	}
	if cfg.Leagues[1].Channel != ChannelLog {
		t.Errorf("Leagues[1].Channel = %q, want log", cfg.Leagues[1].Channel)
	}
}

func TestLoadAcceptsLogChannelWithNoTransportConfig(t *testing.T) {
	setEnv(t, map[string]string{"LEAGUES": `[{"id":1058423,"channel":"log"}]`})

	if _, err := Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
}

func TestLoadRejectsBadLeagues(t *testing.T) {
	tests := []struct {
		name    string
		leagues string
		want    string
	}{
		{
			"missing entirely",
			"",
			"LEAGUES is required",
		},
		{
			"not an array",
			`{"id":1}`,
			"not valid JSON",
		},
		{
			"empty array",
			`[]`,
			"nothing to send",
		},
		{
			"id absent",
			`[{"channel":"log"}]`,
			"id must be a positive integer",
		},
		{
			// Both entries would claim the same partition; only one would send.
			"duplicate id",
			`[{"id":7,"channel":"log"},{"id":7,"channel":"log"}]`,
			"league 7 appears twice",
		},
		{
			"discord without a webhook param",
			`[{"id":7,"channel":"discord"}]`,
			"webhookParam is required for the discord channel",
		},
		{
			"slack without a webhook param",
			`[{"id":7,"channel":"slack"}]`,
			"webhookParam is required for the slack channel",
		},
		{
			"unknown channel",
			`[{"id":7,"channel":"carrier-pigeon"}]`,
			"not one of",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setEnv(t, map[string]string{"LEAGUES": tc.leagues})

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

// A bad league must not mask the one after it — the whole list is reported.
func TestLoadReportsEveryBadLeague(t *testing.T) {
	setEnv(t, map[string]string{"LEAGUES": `[
		{"id":1,"channel":"discord"},
		{"id":2,"channel":"carrier-pigeon"}
	]`})

	_, err := Load()

	if err == nil {
		t.Fatal("Load error = nil, want two validation failures")
	}
	for _, want := range []string{"LEAGUES[0]", "LEAGUES[1]"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want mention of %s", err, want)
		}
	}
}

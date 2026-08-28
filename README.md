# fpl-league-bot

Weekly standings digest and gameweek deadline reminders for Fantasy Premier
League classic leagues. Go on Lambda, OpenTofu, delivered to a Discord or Slack
channel formatted for pasting straight into a WhatsApp group.

## How it works

One Lambda (`cmd/tick`), invoked at 09:00 and 21:00 (Europe/London) by
EventBridge Scheduler. Each run reads the live FPL state and decides for itself
whether anything is due:

| Message | Fires when | Keyed on |
| --- | --- | --- |
| Deadline reminder | the next gameweek deadline is within `reminder_lead_hours` | `SENT#GW#nn#REMINDER` |
| Weekly digest | the latest gameweek flips to `data_checked` (bonus applied) | `SENT#GW#nn#DIGEST` |

Before the first gameweek is scored, `standings.results` is empty and every
manager sits in `new_entries` instead. There is no table to report, so the
digest stays silent until GW1 lands.

Every send is gated on a conditional DynamoDB write, so repeat ticks, Lambda
retries and manual invocations are all no-ops once a message has gone.

### Why one Lambda for every league, not a stack each

The gameweek calendar is the same ~1.6MB response whoever is asking, so a stack
per league would fetch the identical bytes *n* times a tick off an undocumented
free API — and duplicate the table, schedule, alarm, IAM roles and deploy around
it. None of that buys anything: the table is keyed `LEAGUE#<id>`, so the leagues
are already isolated where it matters.

So `app.Fleet` reads the calendar once and hands it to one `app.App` per league.
Everything genuinely per-league stays in the App: its own standings call, its
own state partition, its own sender. Adding a league is one entry in the
`leagues` map and an apply — no migration.

Leagues do not share a fate. `Fleet.Tick` joins their errors rather than
short-circuiting, so a broken webhook on one league still lets the others send,
and each error names the league because a single alarm covers them all. The one
exception is the calendar fetch: nothing can be decided without it, so that
failure stops the tick.

A separate stack per league would be right if they lived in different AWS
accounts, had different owners, or needed different regions. None of that
applies here.

### Why a dumb schedule rather than a weekly cron

Gameweeks are not weekly. In the 2026/27 calendar only 21 of 37 intervals
between deadlines are exactly seven days; the rest range from 2 to 21, and
deadlines land at six different times of day. A per-message cron would drift
immediately. The schedule is therefore deliberately dumb — it only bounds how
late a message can be, never when one is sent.

The end of a gameweek has no timestamp at all. FPL signals it with `finished`
(all fixtures played) and then `data_checked` (bonus points applied, scores
final). The digest waits for `data_checked`, or it would report pre-bonus
totals.

### Why twice daily

`data_checked` flips at whatever hour FPL finishes applying bonus, which can be
the middle of the night. Ticking at 09:00 and 21:00 means the digest lands at an
hour it is plausibly going to be read and pasted into the group, and costs two
bootstrap fetches a day (~1.6MB each) off an undocumented free API instead of
24.

What a coarse tick costs is precision, not correctness:

- **Reminder lead becomes 36–48h** instead of ~48h. The 48h window is wider than
  the 12h tick interval, so it always contains a tick and the reminder is never
  missed — it just fires a little later than nominal. Generally the lead lands
  between `reminder_lead_hours - interval` and `reminder_lead_hours`, which is
  why the interval must stay shorter than the lead.
- **A failed tick costs 12h.** After the scheduler's two retries, the next
  attempt is the other end of the day rather than in an hour.
- **In a compressed week you could get one digest instead of two.** If two
  gameweeks reach `data_checked` inside the same 12h, `latestChecked` reports
  the newer one and the older is skipped — deliberately, because `standings` is
  fetched live, so rendering a skipped gameweek would put today's table under
  last week's heading. Nothing is lost: the digest still shows the correct
  current table, and its footer reads "movement vs GW*n*" against whichever
  snapshot is the real baseline, so the wider span is self-labelling.

Two deadlines can never fall inside one tick interval — the minimum gap between
FPL deadlines is two days — so `nextDeadline` cannot skip a gameweek.

The trade against a single 09:00 tick is that a digest can now arrive at 21:00
instead of always in the morning. That was accepted for the tighter reminder
lead and the faster recovery from a failed tick.

### Movement

`ComputeMovement` prefers our own stored snapshot of last week's table, and
falls back to FPL's `last_rank` when there is no baseline yet. Managers with a
rank of `0` on either side are skipped: that means the start of a phase or a
late joiner, not a 0-place move.

## Layout

```
cmd/tick        Lambda entry point
cmd/preview     renders the current messages locally, sends nothing
internal/fpl    API client and types
internal/app    the "what is due" decision logic — App is one league, Fleet is all of them
internal/digest movement calculation and message rendering
internal/store  DynamoDB single-table state
internal/notify delivery — Discord, Slack or log, behind one Sender interface
infra/          OpenTofu stack
testdata/       captured live responses, refreshed by scripts/capture-fixtures.sh
```

## Setup

```bash
make check                       # fmt, vet, test
make preview LEAGUE=1058423      # see today's messages without sending
```

### Discord

1. In the destination server: Server Settings > Integrations > Webhooks > New
   Webhook, pick the channel, Copy Webhook URL. A server of one works fine.
2. Store the URL yourself, so it never enters Terraform state:

```bash
aws ssm put-parameter --name /fpl-league-bot/discord-webhook \
  --type SecureString --value '<WEBHOOK URL>'
```

The Lambda reads that parameter at cold start; only the parameter *name* is in
the function's environment. The URL embeds the webhook token, so anyone holding
it can post to the channel — rotate by deleting the webhook and creating
another.

### Slack

1. api.slack.com/apps > Create New App > Incoming Webhooks > Add New Webhook to
   Workspace, and pick the channel.
2. Store the URL the same way:

```bash
aws ssm put-parameter --name /fpl-league-bot/slack-webhook \
  --type SecureString --value '<WEBHOOK URL>'
```

An incoming webhook, not a bot token: one secret, one channel, no OAuth scopes
to keep in step. The same cold-start read and the same rotation story as
Discord.

### Deploy

```bash
cp infra/terraform.tfvars.example infra/terraform.tfvars   # fill in the leagues
make apply
make invoke   # run one tick immediately
make logs     # tail
```

Each league is one entry in the `leagues` map:

```hcl
leagues = {
  a = { id = 1058423, channel = "discord", webhook_param = "/fpl-league-bot/a-discord" }
  b = { id = 2222222, channel = "slack",   webhook_param = "/fpl-league-bot/b-slack" }
}
```

Two leagues may name the same `webhook_param` and share a channel — both digests
then land in the one place, each headed by its own league name. That is the
easy way to bring a league up before its own channel exists; moving it later is
two lines and an apply.

## Delivery

`notify.Sender` is one method — `Send(ctx, Message)`. `buildSender` in
`cmd/tick` is the only place that knows which transports exist; everything
upstream sees the interface. Adding one means a file and a `case`.

| `channel` | Notes |
| --- | --- |
| `discord` | Default. Incoming webhook to one channel. URL in SSM. |
| `slack` | Incoming webhook to one channel. URL in SSM. |
| `log` | Prints instead of sending. What `make preview` uses. |

The channel is set per league, and only that channel's settings are required —
so one league can move transport without the others carrying config for it, and
a league on `log` is granted no SSM access at all.

### Discord

**The body is posted raw, carrying its WhatsApp markup.** Discord reads
`*bold*` as italic, so the channel renders it slightly wrong — that is
accepted, because the message menu's **Copy Text** returns the raw source and
the markup survives the paste into WhatsApp. Dragging a selection instead
copies the rendered text and loses it. Wrapping the body in a code block to
make Discord display it literally was tried and rejected as harder to read.

Every post sets `SUPPRESS_EMBEDS`, so the reminder's link is not unfurled into
a preview card that gets copied along with the text, and sends
`allowed_mentions.parse: []`, because a manager can name their team
`@everyone`. Messages over the 2000-character limit are split on line
boundaries so a table never breaks mid-row.

### Slack

**mrkdwn is left on.** This channel is read in Slack rather than copied on into
WhatsApp, so there is nothing to protect: Slack's `*bold*`, `_italic_` and
bare-URL autolinking already match what `digest` renders, and the bodies need
no translation. Block Kit was considered and skipped — a `header` block would
duplicate the heading the body already opens with, and a `context` footer would
mean the sender parsing the body back apart. Both trade a second renderer for
larger type on one line.

The body is HTML-escaped first: `&`, `<` and `>` are reserved in Slack message
text, and team names come from managers, so "Salah & Co" would otherwise render
wrong and `<Wildcard>` would vanish. Escaping `<` is also why no
`allowed_mentions` equivalent is needed — a literal `@everyone` is inert in
Slack, and no team name can spell the `<!everyone>` form that is not. Escaping
happens *before* the split, since the 3000-rune limit applies to what is
actually posted and an escaped `&` costs five runes. Every post sends
`unfurl_links` and `unfurl_media` false, the `SUPPRESS_EMBEDS` equivalent.

## The API

Undocumented, unversioned, no authentication. Private league standings are
readable by ID — membership is not checked.

Requests send a browser `User-Agent`; the default Go one gets 403'd
intermittently. The client retries 5xx and 429 with backoff, because the API
goes read-only during price changes (~02:00 UK) and gameweek processing.

Fields gain and disappear between seasons, so the flags the app branches on are
pointers and `Validate` rejects a response that omits them — a schema change
fails loudly rather than silently deciding "no digest this week". When a season
rolls over, run `make capture` and then the tests.

## Why the copy-paste step

The WhatsApp Cloud API is business-to-individual only: there is no
group-messaging endpoint at any tier, so an automated post into a group chat is
not possible. The group is where the conversation happens and moving twenty
people to another app is a social problem, not a technical one — so the bot
delivers to a Discord channel and the last hop is manual.

Chat app to chat app beats mail client to chat app on a phone. An SES email
transport existed in the MVP and was removed once Discord proved out — with it
went the SES identities, the `ses:SendEmail` grant, and the SNS topic that used
to email Lambda alarms. The `-tick-errors` CloudWatch alarm remains, with no
notification action: check it or `make logs` if a digest never lands.

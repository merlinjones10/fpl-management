# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
make check                    # gofmt -l gate, go vet, go test ./...  — run before every commit
make test                     # go test ./...
go test ./internal/digest -run TestRenderDigest -v   # single test
make preview LEAGUE=1058423   # one tick against the live API, prints messages, sends nothing
make build                    # linux/arm64 bootstrap into dist/ (required before plan/apply)
make plan / make apply        # OpenTofu (`tofu`, not `terraform`) in infra/
make invoke                   # invoke the deployed Lambda once
make logs                     # tail CloudWatch
make capture LEAGUE=...       # refresh testdata from live API
```

`make check` fails on any unformatted file — `gofmt -w .` before committing.

## Architecture

One Lambda (`cmd/tick`) invoked at 09:00 and 21:00 Europe/London by EventBridge
Scheduler. There is no per-message schedule: the tick reads live FPL state and
decides for itself what is due. Full rationale (why twice daily and what that
costs, why `data_checked`, why the manual copy-paste to WhatsApp) is in
`README.md` — read it before changing scheduling or delivery behaviour.

The tick interval must stay shorter than `reminder_lead_hours`, or a reminder
can fall between two ticks. `TestScheduledTicksNeverMissAReminder` guards this.

Dependency flow: `cmd/tick` wires everything → `internal/app` orchestrates →
`fpl` (read) + `store` (state) + `digest` (render) + `notify` (deliver).
`internal/app` depends on `FPLClient`, `Store` and `notify.Sender` interfaces
declared in `app.go`, so all of `Tick` is unit-testable with fakes.

### Invariants worth preserving

- **Every send is gated on a conditional DynamoDB write** (`store.Claim`,
  `attribute_not_exists(pk)`). This is the only thing making repeat ticks,
  Lambda retries and manual invokes safe. On send failure `app.send` calls
  `Release` so the next tick retries. Never bypass Claim/Release.
- **The snapshot is written only after a successful send**, so a failed week
  never becomes the movement baseline for the next one.
- **Reminder runs before digest and their errors are `errors.Join`ed**, not
  short-circuited — a digest failure must not suppress a time-critical
  reminder. Both can fire in one tick.
- **Digest waits for `data_checked`, not `finished`** — `finished` means all
  fixtures played, `data_checked` means bonus applied. Using the former reports
  pre-bonus totals.
- **FPL flags the app branches on are `*bool`, and `Validate` rejects nil.**
  The API is unversioned; a removed field would decode to `false` and silently
  change a decision. Any new field the app branches on gets the same treatment.
- **Rank `0` means "no rank"** (start of a phase, or a late joiner), not a
  0-place move — `ComputeMovement` skips those rows on either side.
- **The Discord body is posted raw, with its WhatsApp markup.** Discord renders
  `*bold*` as italic, which is accepted: the message menu's "Copy Text" returns
  the raw source, so the markup survives the paste into WhatsApp. A code-block
  wrapper was tried and rejected as harder to read. Every post also sets
  `SUPPRESS_EMBEDS` (a copied link preview) and empty `allowed_mentions.parse`
  (a team named `@everyone`).
- **Slack is the opposite case and leaves mrkdwn on.** That channel is read in
  Slack, not copied on, and Slack's `*bold*`/`_italic_` already match the
  rendered markup — so no translation, and no Block Kit (a `header` would
  duplicate the body's own heading). The body *is* HTML-escaped (`&`, `<`, `>`)
  because team names are user-supplied, and escaped **before** `splitMessage`,
  since the limit applies to what is posted.
- **Secrets stay out of Terraform state and function config.** The Discord
  webhook URL lives in an SSM SecureString created out of band; only the
  parameter *name* is in the Lambda environment, read at cold start by
  `secureParam`. The URL embeds the webhook token, so it never enters an error
  string or log line either.

### DynamoDB single-table layout

```
pk = LEAGUE#<id>
sk = SENT#GW#<nn>#DIGEST | SENT#GW#<nn>#REMINDER   idempotency markers
     SNAPSHOT#GW#<nn>                              movement baseline
```

Gameweeks are zero-padded (`gwKey`) so `sk` range queries sort GW9 before GW10.
No TTL — an expiring idempotency marker would let an old message resend.

### Adding a delivery channel

Three edits: a `notify.Sender` implementation, a `config.Channel` const plus
its required-settings `case` in `config.Load`, and a `case` in
`buildSender` (`cmd/tick/main.go`). Nothing upstream of the interface changes.
Terraform gates channel resources and IAM statements on `local.use_*` in
`infra/locals.tf` / `infra/iam.tf`, plus the `notify_channel` validation and
the `check "channel_settings"` block. A webhook-style channel only needs its
parameter folded into `local.webhook_param`; the IAM grant follows from
`local.webhook_param_arns`.

## Testing

Table-driven, stdlib only — no assertion libraries. Fakes are hand-written in
the `_test.go` file that uses them. HTTP transports are tested against
`httptest` servers via the `WithBaseURL` option; the Discord sender takes its
full webhook URL, so its tests point that at the test server directly.

`testdata/` holds captured live API responses (`standings-preseason.json` is
the pre-GW1 state where `standings.results` is empty and everyone sits in
`new_entries`; `bootstrap-events.json` is the gameweek calendar, projected down
from the ~1.6MB bootstrap payload). When a season rolls over, run `make capture`
then the tests — a decode or `Validate` failure is the early warning that the
API shape moved. Note `scripts/capture-fixtures.sh` writes
`standings-live.json`, not over the committed pre-season fixture; promote it
deliberately.

## Infra notes

- `infra/` is OpenTofu with **local state**, deliberately (single operator).
- `dist/bootstrap` must exist before `plan`/`apply` — the `archive_file` data
  source reads it. `make plan` and `make apply` build first.
- Runtime is `provided.al2023` on arm64; the binary must be named `bootstrap`.
- `cmd/tick` and `cmd/preview` both blank-import `time/tzdata` — the runtime
  carries no zoneinfo and `time.LoadLocation("Europe/London")` would fail at
  startup without it.
- `infra/terraform.tfvars` is gitignored; `terraform.tfvars.example` is the
  template.

## Diagram

`docs/flow.swimlanes` is a swimlanes.io source describing one tick. Keep it in
step with changes to the tick's decision flow.

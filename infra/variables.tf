variable "region" {
  description = "AWS region."
  type        = string
  default     = "eu-west-2"
}

variable "name_prefix" {
  description = "Prefix for every resource name."
  type        = string
  default     = "fpl-league-bot"
}

variable "leagues" {
  description = <<-EOT
    The leagues to run, keyed by a short name that appears only in plan output.
    One Lambda serves all of them: the gameweek calendar is the same fetch for
    every league, and each gets its own DynamoDB partition and its own sender.

      leagues = {
        a = { id = 1058423, channel = "discord", webhook_param = "/fpl-league-bot/a-discord" }
        b = { id = 2222222, channel = "slack",   webhook_param = "/fpl-league-bot/b-slack" }
      }

    id      FPL classic league ID, from the league URL on the FPL site.
    channel discord, slack or log. Defaults to discord.
    webhook_param
            Name of an SSM SecureString holding that channel's webhook URL —
            never the URL itself, which embeds its token and must not reach
            Terraform state. Create the webhook (Discord: Server Settings >
            Integrations > Webhooks; Slack: a Slack app with Incoming Webhooks
            enabled), then store it out of band:

              aws ssm put-parameter --name /fpl-league-bot/a-discord \
                --type SecureString --value '<WEBHOOK URL>'

    Two leagues may name the same parameter and share a channel — which is how
    a new league runs into the existing one until its own is ready. The log
    channel needs no parameter and is granted nothing.
  EOT

  type = map(object({
    id            = number
    channel       = optional(string, "discord")
    webhook_param = optional(string, "")
  }))

  validation {
    condition     = length(var.leagues) > 0
    error_message = "At least one league is required."
  }

  validation {
    condition     = alltrue([for l in var.leagues : l.id > 0])
    error_message = "Each league needs a positive id, taken from its URL on the FPL site."
  }

  validation {
    condition     = alltrue([for l in var.leagues : contains(["discord", "slack", "log"], l.channel)])
    error_message = "Each league's channel must be one of: discord, slack, log."
  }

  validation {
    # Two entries for one league would both claim the same partition: one
    # sends, the other silently loses and looks like a quiet week.
    condition     = length(distinct([for l in var.leagues : l.id])) == length(var.leagues)
    error_message = "Each league id must appear only once."
  }
}

variable "reminder_lead_hours" {
  description = "How far ahead of a gameweek deadline the reminder fires."
  type        = number
  default     = 48

  validation {
    condition     = var.reminder_lead_hours > 0 && var.reminder_lead_hours <= 168
    error_message = "reminder_lead_hours must be between 0 and 168."
  }
}

variable "timezone" {
  description = "IANA zone used to render deadlines. FPL returns them in UTC."
  type        = string
  default     = "Europe/London"
}

variable "schedule_expression" {
  description = <<-EOT
    When to evaluate, in var.timezone. The Lambda decides for itself whether
    anything is due, so this only bounds how late a message can be — not when
    one is sent.

    09:00 and 21:00 rather than hourly: those are the hours the digest actually
    gets pasted into the group, and it is two bootstrap fetches a day off an
    undocumented API instead of 24. The cost is precision, not correctness —
    see "Why twice daily" in README.md before changing it.

    Whatever you set, keep the interval shorter than reminder_lead_hours. The
    lead window has to be wider than the gap between ticks or a reminder can
    fall between two of them.
  EOT
  type        = string
  default     = "cron(0 9,21 * * ? *)"
}

variable "log_retention_days" {
  description = "CloudWatch log retention."
  type        = number
  default     = 30
}

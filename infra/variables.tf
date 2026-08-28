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

variable "league_id" {
  description = "FPL classic league ID. Found in the league URL on the FPL site."
  type        = number
}

variable "notify_channel" {
  description = <<-EOT
    Delivery transport: discord, slack or log. Only the selected channel's
    resources are created and only its settings are required.
  EOT
  type        = string
  default     = "discord"

  validation {
    condition     = contains(["discord", "slack", "log"], var.notify_channel)
    error_message = "notify_channel must be one of: discord, slack, log."
  }
}

variable "discord_webhook_param" {
  description = <<-EOT
    Name of an SSM SecureString holding the channel's webhook URL — not the URL
    itself, which embeds the webhook token and must never reach Terraform state.
    Create the webhook under Server Settings > Integrations > Webhooks, then:

      aws ssm put-parameter --name /fpl-league-bot/discord-webhook \
        --type SecureString --value 'https://discord.com/api/webhooks/<id>/<token>'
  EOT
  type        = string
  default     = "/fpl-league-bot/discord-webhook"
}

variable "slack_webhook_param" {
  description = <<-EOT
    Name of an SSM SecureString holding the channel's incoming webhook URL — not
    the URL itself, which embeds the webhook token and must never reach
    Terraform state. Create a Slack app, enable Incoming Webhooks, add one to
    the destination channel, then:

      aws ssm put-parameter --name /fpl-league-bot/slack-webhook \
        --type SecureString --value 'https://hooks.slack.com/services/<T>/<B>/<token>'
  EOT
  type        = string
  default     = "/fpl-league-bot/slack-webhook"
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

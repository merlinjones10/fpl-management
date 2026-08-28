locals {
  use_discord = var.notify_channel == "discord"
  use_slack   = var.notify_channel == "slack"

  # The webhook-bearing channels differ only in which parameter holds the URL,
  # so the IAM grant is written once against whichever one is selected.
  webhook_param = local.use_discord ? var.discord_webhook_param : (local.use_slack ? var.slack_webhook_param : "")

  # Built rather than looked up: reading the parameter would pull the webhook URL
  # into Terraform state, which is the one thing this arrangement avoids.
  #
  # A list of at most one, so the log channel grants nothing at all.
  webhook_param_arns = local.webhook_param == "" ? [] : [format(
    "arn:aws:ssm:%s:%s:parameter/%s",
    var.region,
    data.aws_caller_identity.current.account_id,
    trimprefix(local.webhook_param, "/"),
  )]
}

# Fail at plan time rather than on the first invocation.
check "channel_settings" {
  assert {
    condition     = !local.use_discord || var.discord_webhook_param != ""
    error_message = "discord_webhook_param is required when notify_channel is discord."
  }

  assert {
    condition     = !local.use_slack || var.slack_webhook_param != ""
    error_message = "slack_webhook_param is required when notify_channel is slack."
  }
}

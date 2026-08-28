locals {
  use_discord = var.notify_channel == "discord"

  # Built rather than looked up: reading the parameter would pull the webhook URL
  # into Terraform state, which is the one thing this arrangement avoids.
  discord_webhook_param_arn = format(
    "arn:aws:ssm:%s:%s:parameter/%s",
    var.region,
    data.aws_caller_identity.current.account_id,
    trimprefix(var.discord_webhook_param, "/"),
  )
}

# Fail at plan time rather than on the first invocation.
check "channel_settings" {
  assert {
    condition     = !local.use_discord || var.discord_webhook_param != ""
    error_message = "discord_webhook_param is required when notify_channel is discord."
  }
}

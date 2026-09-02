# The live configuration. Committed, because it holds no secrets: league IDs
# are public, and webhook_param is the *name* of an SSM SecureString, never the
# URL. Store each URL yourself, so it never enters Terraform state:
#
#   aws ssm put-parameter --name /fpl-league-bot/a-discord \
#     --type SecureString --value '<WEBHOOK URL>'
#
# Discord: Server Settings > Integrations > Webhooks > New Webhook.
# Slack:   api.slack.com/apps > Incoming Webhooks > Add New Webhook to Workspace.
#
# One Lambda serves every league here. Each gets its own DynamoDB partition and
# its own sender, so they can sit on different channels — or share one.
#
# Copy a message out with Discord's message menu > "Copy Text" — that returns
# the raw source, so *bold* survives the paste into WhatsApp. Dragging a
# selection does not: Discord copies the rendered text and drops the markup.
leagues = {
  a = {
    id            = 1058423
    channel       = "discord"
    webhook_param = "/fpl-league-bot/discord-webhook"
  }

  # TODO: fill in the new league's ID. Sharing league A's webhook for now, so
  # both digests land in the same Discord channel, each headed by its league
  # name. When the Slack side is ready, this becomes:
  #   channel       = "slack"
  #   webhook_param = "/fpl-league-bot/b-slack"
  # and the parameter goes in with `aws ssm put-parameter --type SecureString`.
  b = {
    id            = 486771
    channel       = "slack"
    webhook_param = "/fpl-league-bot/league-b-webhook"
  }
}

# --- Optional -------------------------------------------------------------
# reminder_lead_hours = 48
# schedule_expression = "cron(0 * * * ? *)"     # hourly, in var.timezone (the default)

locals {
  # The Lambda takes one env var, not a set per league. Map iteration is
  # lexicographic by key, so the order is stable and re-editing the map does
  # not churn the plan. Parameter names travel here; the URLs behind them
  # never do.
  leagues_json = jsonencode([
    for k, l in var.leagues : {
      id           = l.id
      channel      = l.channel
      webhookParam = l.webhook_param
    }
  ])

  # Every distinct parameter the fleet will read. Two leagues sharing a webhook
  # collapse to one grant; a league on the log channel contributes none.
  webhook_params = distinct([
    for l in var.leagues : l.webhook_param if l.webhook_param != ""
  ])

  # Built rather than looked up: reading the parameter would pull the webhook URL
  # into Terraform state, which is the one thing this arrangement avoids.
  webhook_param_arns = [
    for p in local.webhook_params : format(
      "arn:aws:ssm:%s:%s:parameter/%s",
      var.region,
      data.aws_caller_identity.current.account_id,
      trimprefix(p, "/"),
    )
  ]
}

# Fail at plan time rather than on the first invocation.
check "channel_settings" {
  assert {
    condition = alltrue([
      for l in var.leagues :
      l.webhook_param != "" if contains(["discord", "slack"], l.channel)
    ])
    error_message = "Every league on discord or slack needs a webhook_param."
  }
}

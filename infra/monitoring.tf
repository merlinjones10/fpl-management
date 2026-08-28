# The failure mode that matters is silence: a broken tick sends nothing and
# looks identical to a quiet week. Any error is worth knowing about.
#
# No alarm_actions: there is no notification transport in the stack, so this is
# a console and CloudWatch-dashboard signal only. Check it, or `make logs`, if a
# digest never lands.
resource "aws_cloudwatch_metric_alarm" "errors" {
  alarm_name          = "${var.name_prefix}-tick-errors"
  alarm_description   = "The FPL tick Lambda failed. Reminders and digests may be missing."
  namespace           = "AWS/Lambda"
  metric_name         = "Errors"
  statistic           = "Sum"
  period              = 3600
  evaluation_periods  = 1
  threshold           = 1
  comparison_operator = "GreaterThanOrEqualToThreshold"
  treat_missing_data  = "notBreaching"

  dimensions = {
    FunctionName = aws_lambda_function.tick.function_name
  }
}

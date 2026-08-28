# FPL deadlines do not follow the calendar — double gameweeks, blanks,
# international breaks and Boxing Day all move them. So this schedule is
# deliberately dumb: tick on a fixed clock, and let the Lambda read the real
# deadlines and decide. The interval only bounds how late a message can be.
#
# schedule_expression_timezone means the 09:00/21:00 default tracks BST rather
# than drifting an hour twice a year.
resource "aws_scheduler_schedule" "tick" {
  name = "${var.name_prefix}-tick"

  schedule_expression          = var.schedule_expression
  schedule_expression_timezone = var.timezone

  flexible_time_window {
    mode = "OFF"
  }

  target {
    arn      = aws_lambda_function.tick.arn
    role_arn = aws_iam_role.scheduler.arn

    retry_policy {
      maximum_retry_attempts = 2
    }
  }
}

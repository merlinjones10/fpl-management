locals {
  binary_path = "${path.module}/../dist/bootstrap"
  zip_path    = "${path.module}/../dist/tick.zip"
}

# Built by `make build` before apply. provided.al2023 expects the executable to
# be named exactly `bootstrap`.
data "archive_file" "tick" {
  type        = "zip"
  source_file = local.binary_path
  output_path = local.zip_path
}

resource "aws_cloudwatch_log_group" "lambda" {
  name              = "/aws/lambda/${var.name_prefix}-tick"
  retention_in_days = var.log_retention_days
}

resource "aws_lambda_function" "tick" {
  function_name = "${var.name_prefix}-tick"
  role          = aws_iam_role.lambda.arn

  filename         = data.archive_file.tick.output_path
  source_code_hash = data.archive_file.tick.output_base64sha256

  runtime       = "provided.al2023"
  handler       = "bootstrap"
  architectures = ["arm64"]

  # One ~1.6MB bootstrap fetch shared by every league, then a standings call,
  # some DynamoDB and at most two webhook POSTs per league. Memory is well
  # above what it needs because CPU scales with it and JSON decoding is the
  # bottleneck; adding a league costs a small fraction of the timeout.
  memory_size = 512
  timeout     = 60

  environment {
    variables = {
      TABLE_NAME = aws_dynamodb_table.state.name

      # Every league, with its channel and its parameter *name* — never a
      # webhook URL. Function config is readable by anyone holding
      # lambda:GetFunction, and only the named parameters are grantable in IAM.
      LEAGUES = local.leagues_json

      REMINDER_LEAD_HOURS = tostring(var.reminder_lead_hours)
      TIMEZONE            = var.timezone
    }
  }

  depends_on = [
    aws_iam_role_policy.lambda,
    aws_cloudwatch_log_group.lambda,
  ]
}

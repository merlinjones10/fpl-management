output "function_name" {
  description = "Invoke manually with: aws lambda invoke --function-name <this> /dev/stdout"
  value       = aws_lambda_function.tick.function_name
}

output "table_name" {
  value = aws_dynamodb_table.state.name
}

output "log_group" {
  value = aws_cloudwatch_log_group.lambda.name
}

output "schedule" {
  value = aws_scheduler_schedule.tick.schedule_expression
}

output "notify_channel" {
  value = var.notify_channel
}

output "discord_webhook_param" {
  description = "Create this SecureString before the first invocation; the Lambda reads it at cold start."
  value       = local.use_discord ? var.discord_webhook_param : ""
}

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

output "leagues" {
  description = "Each league and the transport its messages go out on."
  value       = { for k, l in var.leagues : k => "${l.id} → ${l.channel}" }
}

output "webhook_params" {
  description = "Create these SecureStrings before the first invocation; the Lambda reads them at cold start."
  value       = local.webhook_params
}

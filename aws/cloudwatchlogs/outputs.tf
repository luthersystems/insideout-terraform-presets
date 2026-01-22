output "log_group_name" {
  value       = aws_cloudwatch_log_group.app.name
  description = "CloudWatch log group name"
}

output "log_group_arn" {
  value       = aws_cloudwatch_log_group.app.arn
  description = "CloudWatch log group ARN"
}

output "writer_role_arn" {
  value       = aws_iam_role.writer.arn
  description = "IAM role to attach to workloads that write logs"
}

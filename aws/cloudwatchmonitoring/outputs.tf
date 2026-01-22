output "sns_topic_arn" {
  value       = aws_sns_topic.alarms.arn
  description = "SNS topic ARN for CloudWatch alarms"
}

output "dashboard_name" {
  value       = aws_cloudwatch_dashboard.main.dashboard_name
  description = "CloudWatch dashboard name"
}

# Union of all created alarms (only non-empty sets contribute)
output "alarm_arns" {
  value = concat(
    [for a in aws_cloudwatch_metric_alarm.ec2_cpu_high : a.arn],
    [for a in aws_cloudwatch_metric_alarm.rds_cpu_high : a.arn],
    [for a in aws_cloudwatch_metric_alarm.rds_free_storage_low : a.arn],
    [for a in aws_cloudwatch_metric_alarm.redis_cpu_high : a.arn],
    [for a in aws_cloudwatch_metric_alarm.sqs_backlog : a.arn]
  )
  description = "List of created alarm ARNs"
}

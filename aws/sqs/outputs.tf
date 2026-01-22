output "queue_name" {
  value       = aws_sqs_queue.this.name
  description = "SQS queue name"
}

output "queue_url" {
  value       = aws_sqs_queue.this.url
  description = "SQS queue URL"
}

output "queue_arn" {
  value       = aws_sqs_queue.this.arn
  description = "SQS queue ARN"
}

output "dlq_name" {
  value       = try(aws_sqs_queue.dlq[0].name, null)
  description = "DLQ name (null if disabled)"
}

output "dlq_url" {
  value       = try(aws_sqs_queue.dlq[0].url, null)
  description = "DLQ URL (null if disabled)"
}

output "dlq_arn" {
  value       = try(aws_sqs_queue.dlq[0].arn, null)
  description = "DLQ ARN (null if disabled)"
}

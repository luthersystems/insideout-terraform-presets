output "table_name" {
  value       = aws_dynamodb_table.this.name
  description = "DynamoDB table name"
}

output "table_arn" {
  value       = aws_dynamodb_table.this.arn
  description = "DynamoDB table ARN"
}

output "stream_arn" {
  value       = aws_dynamodb_table.this.stream_arn
  description = "Stream ARN (null when streams disabled)"
}

output "stream_label" {
  value       = aws_dynamodb_table.this.stream_label
  description = "Stream label (null when streams disabled)"
}

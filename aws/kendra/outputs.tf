output "index_id" {
  value       = aws_kendra_index.this.id
  description = "ID of the Kendra index."
}

output "index_arn" {
  value       = aws_kendra_index.this.arn
  description = "ARN of the Kendra index."
}

output "kendra_role_arn" {
  value       = aws_iam_role.index.arn
  description = "ARN of the IAM role Kendra assumes to write the index's CloudWatch logs and metrics."
}

output "data_source_id" {
  value       = local.has_s3_source ? aws_kendra_data_source.s3[0].data_source_id : null
  description = "ID of the S3 data-source connector. null when no s3_bucket_name was wired in (bare index)."
}

output "data_source_role_arn" {
  value       = local.has_s3_source ? aws_iam_role.data_source[0].arn : null
  description = "ARN of the IAM role Kendra assumes to crawl the S3 source bucket. null when no S3 data source is wired in."
}

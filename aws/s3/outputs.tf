output "bucket_name" {
  value       = aws_s3_bucket.this.bucket
  description = "S3 bucket name"
}

output "bucket_arn" {
  value       = aws_s3_bucket.this.arn
  description = "S3 bucket ARN"
}

output "bucket_domain_name" {
  value       = aws_s3_bucket.this.bucket_domain_name
  description = "Global bucket domain name"
}

output "bucket_regional_domain_name" {
  value       = aws_s3_bucket.this.bucket_regional_domain_name
  description = "Regional bucket domain name"
}

output "kms_key_arn" {
  value       = var.kms_key_arn
  description = "The kms_key_arn the bucket is encrypted with (a key or alias ARN), or null for SSE-S3. The composer wires it to the readers it knows (aws/bedrock, aws/kendra) so their roles get kms:Decrypt; any other reader needs that grant from the caller (#909)."
}

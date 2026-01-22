output "distribution_id" {
  value       = aws_cloudfront_distribution.this.id
  description = "CloudFront distribution ID"
}

output "distribution_arn" {
  value       = aws_cloudfront_distribution.this.arn
  description = "CloudFront distribution ARN"
}

output "domain_name" {
  value       = aws_cloudfront_distribution.this.domain_name
  description = "CloudFront domain name"
}

output "origin_id" {
  value       = local.origin_id
  description = "Origin ID used in the distribution"
}

output "origin_bucket_name" {
  value       = try(aws_s3_bucket.origin[0].bucket, null)
  description = "Created S3 origin bucket name (if create_bucket=true)"
}

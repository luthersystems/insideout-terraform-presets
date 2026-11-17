output "user_pool_id" {
  value       = aws_cognito_user_pool.this.id
  description = "Cognito User Pool ID"
}

output "user_pool_client_id" {
  value       = aws_cognito_user_pool_client.web.id
  description = "Cognito app client ID"
}

output "hosted_ui_domain" {
  value       = try(aws_cognito_user_pool_domain.this[0].domain, null)
  description = "Hosted UI domain prefix (null if not created)"
}

output "issuer_url" {
  value       = try("https://${aws_cognito_user_pool_domain.this[0].domain}.auth.${var.region}.amazoncognito.com", null)
  description = "OIDC issuer/hosted UI base URL (null if domain disabled)"
}

output "federated_identity_providers" {
  value = concat(
    [for k, _ in aws_cognito_identity_provider.oidc : k],
    [for k, _ in aws_cognito_identity_provider.saml : k]
  )
  description = "Names of configured OIDC/SAML identity providers"
}

# Alias target of the custom domain, for a Route 53 alias record (#925).
# All null when custom_domain is null.
output "custom_domain_cloudfront_distribution" {
  value       = try(aws_cognito_user_pool_domain.custom[0].cloudfront_distribution, null)
  description = "CloudFront domain name serving custom_domain (null if not set). Use as a Route 53 alias target."
}

output "custom_domain_cloudfront_distribution_arn" {
  value       = try(aws_cognito_user_pool_domain.custom[0].cloudfront_distribution_arn, null)
  description = "ARN of the CloudFront distribution serving custom_domain (null if not set)."
}

output "custom_domain_cloudfront_zone_id" {
  value       = try(aws_cognito_user_pool_domain.custom[0].cloudfront_distribution_zone_id, null)
  description = "Hosted zone ID of that CloudFront distribution (null if not set). Use as a Route 53 alias zone ID."
}

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

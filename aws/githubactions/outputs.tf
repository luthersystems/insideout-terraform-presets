output "role_arn" {
  description = "ARN of the deploy role. Set it as `role-to-assume` in aws-actions/configure-aws-credentials; the workflow needs `permissions: id-token: write`."
  value       = aws_iam_role.deploy.arn
}

output "role_name" {
  description = "Name of the deploy role. Attach more policies to it here or with managed_policy_arns."
  value       = aws_iam_role.deploy.name
}

output "oidc_provider_arn" {
  description = "ARN of the GitHub OIDC provider the role trusts: created here, given, or found in the account."
  value       = local.oidc_provider_arn
}

output "allowed_subjects" {
  description = "The token sub claims the role admits."
  value       = local.subjects
}

output "tags" {
  description = "The Project-tagged map applied to every taggable resource in this preset."
  value       = local.tags
}

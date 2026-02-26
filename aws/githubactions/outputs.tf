output "repository_name" {
  description = "Name of the GitHub repository"
  value       = github_repository.repo.name
}

output "repository_full_name" {
  description = "Full name of the GitHub repository (org/repo)"
  value       = github_repository.repo.full_name
}

output "repository_html_url" {
  description = "URL of the GitHub repository"
  value       = github_repository.repo.html_url
}

output "oidc_provider_arn" {
  description = "ARN of the GitHub OIDC provider"
  value       = local.oidc_provider_arn
}

output "iam_role_arn" {
  description = "ARN of the IAM role for GitHub Actions"
  value       = aws_iam_role.github_actions.arn
}

output "iam_role_name" {
  description = "Name of the IAM role for GitHub Actions"
  value       = aws_iam_role.github_actions.name
}

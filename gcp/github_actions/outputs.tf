output "service_account_email" {
  value       = google_service_account.deploy.email
  description = "Email of the deploy SA. Paste into the GitHub Actions workflow as the `service_account` input on google-github-actions/auth@v2."
}

output "service_account_name" {
  value       = google_service_account.deploy.name
  description = "Fully-qualified SA resource name (projects/<id>/serviceAccounts/<email>). Useful for tooling that needs the GCP resource path rather than just the email."
}

output "workload_identity_provider" {
  value       = "projects/${data.google_project.this.number}/locations/global/workloadIdentityPools/${google_iam_workload_identity_pool.github.workload_identity_pool_id}/providers/${google_iam_workload_identity_pool_provider.github.workload_identity_pool_provider_id}"
  description = "Fully-qualified WIF provider name. Paste into the GitHub Actions workflow as the `workload_identity_provider` input on google-github-actions/auth@v2 — e.g.:\n\n  - uses: google-github-actions/auth@v2\n    with:\n      workload_identity_provider: <this output>\n      service_account: <service_account_email output>\n"
}

output "pool_name" {
  value       = google_iam_workload_identity_pool.github.name
  description = "Fully-qualified WIF pool resource name (projects/<num>/locations/global/workloadIdentityPools/<id>)."
}

output "pool_id" {
  value       = google_iam_workload_identity_pool.github.workload_identity_pool_id
  description = "Short pool ID (var.project-prefixed)."
}

output "provider_id" {
  value       = google_iam_workload_identity_pool_provider.github.workload_identity_pool_provider_id
  description = "Short provider ID within the pool (var.project-prefixed)."
}

output "github_repository" {
  value       = var.github_repository
  description = "Pass-through of the configured GitHub repository (OWNER/REPO)."
}

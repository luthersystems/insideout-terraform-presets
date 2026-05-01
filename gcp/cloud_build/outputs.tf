output "trigger_id" {
  value       = google_cloudbuild_trigger.trigger.id
  description = "The ID of the Cloud Build trigger"
}

output "trigger_name" {
  value       = google_cloudbuild_trigger.trigger.name
  description = "The name of the Cloud Build trigger"
}

output "webhook_secret_name" {
  value       = google_secret_manager_secret.webhook.secret_id
  description = "Secret Manager secret holding the webhook trigger token. Retrieve with: gcloud secrets versions access latest --secret=<this>"
}

output "cloudbuild_runner_service_account_email" {
  value       = google_service_account.cloudbuild_runner.email
  description = "Email of the BYOSA runner SA the Cloud Build trigger executes as. Grant additional roles here for downstream resources the trigger's build steps must touch."
}

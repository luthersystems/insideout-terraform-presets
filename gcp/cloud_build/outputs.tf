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

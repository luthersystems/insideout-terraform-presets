output "trigger_id" {
  value       = google_cloudbuild_trigger.trigger.id
  description = "The ID of the Cloud Build trigger"
}

output "trigger_name" {
  value       = google_cloudbuild_trigger.trigger.name
  description = "The name of the Cloud Build trigger"
}

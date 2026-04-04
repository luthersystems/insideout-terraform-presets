output "function_name" {
  description = "The name of the Cloud Function"
  value       = google_cloudfunctions2_function.this.name
}

output "function_uri" {
  description = "The URI of the Cloud Function"
  value       = google_cloudfunctions2_function.this.service_config[0].uri
}

output "function_url" {
  description = "The HTTPS URL of the Cloud Function"
  value       = google_cloudfunctions2_function.this.url
}

output "service_account_email" {
  description = "The service account email used by the function"
  value       = google_cloudfunctions2_function.this.service_config[0].service_account_email
}

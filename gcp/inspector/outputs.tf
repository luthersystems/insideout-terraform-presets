output "service_account_email" {
  description = "Inspector service account email"
  value       = google_service_account.inspector.email
}

output "service_account_id" {
  description = "Inspector service account ID"
  value       = google_service_account.inspector.id
}

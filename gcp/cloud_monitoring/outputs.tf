output "dashboard_id" {
  value       = google_monitoring_dashboard.dashboard.id
  description = "The ID of the monitoring dashboard"
}

# notification_channels exposes Cloud Monitoring channel resource
# names for the channels created from var.notification_channel_emails.
# Per-component alert policies in gcp/<module>/observability.tf
# consume this list via the composer's post-switch wiring loop (#204).
output "notification_channels" {
  value       = [for c in google_monitoring_notification_channel.email : c.name]
  description = "Cloud Monitoring notification channel resource names. Empty when var.notification_channel_emails is empty."
}

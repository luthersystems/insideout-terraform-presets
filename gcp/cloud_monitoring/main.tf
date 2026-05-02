terraform {
  required_version = ">= 1.5"
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = ">= 5.0"
    }
  }
}

# The pre-#168 dashboard shipped with widgets = [] which the Monitoring API
# rejects ("Dashboard must contain at least one widget"). Default now ships
# a minimal-but-API-valid widget so the module composes cleanly out of the
# box; override var.dashboard_json with your own spec for a real dashboard.
resource "google_monitoring_dashboard" "dashboard" {
  project        = var.project_id
  dashboard_json = var.dashboard_json
}

# Email notification channels (issue #204) — opt-in via
# var.notification_channel_emails. Per-component alert policies in
# gcp/<module>/observability.tf consume the notification_channels
# output below. Composer wires that output into every emitter when
# gcp_cloud_monitoring is selected (pkg/composer/contracts.go
# post-switch loop).
resource "google_monitoring_notification_channel" "email" {
  for_each = toset(var.notification_channel_emails)

  project      = var.project_id
  display_name = "email:${each.value}"
  type         = "email"
  labels = {
    email_address = each.value
  }
  user_labels = merge({ project = var.project }, var.labels)
}

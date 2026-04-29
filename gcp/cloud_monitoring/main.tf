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

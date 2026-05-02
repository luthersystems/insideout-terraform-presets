# tflint-ignore: terraform_unused_declarations  # composer always wires var.project at the root (CLAUDE.md mandate)
variable "project" {
  description = "Naming/label prefix for stack resources (NOT a GCP project ID — see var.project_id)."
  type        = string

  validation {
    condition     = length(trimspace(var.project)) > 0
    error_message = "project must be a non-empty string."
  }
}

variable "project_id" {
  description = "Real GCP project ID where the dashboard is created. Distinct from var.project, which is the naming prefix and need not be a valid GCP project ID."
  type        = string

  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{4,28}[a-z0-9]$", var.project_id))
    error_message = "project_id must be a valid GCP project ID: 6–30 chars, lowercase letters/digits/hyphens, start with a letter, end alphanumeric."
  }
}

# tflint-ignore: terraform_unused_declarations  # composer always wires var.region at the root (CLAUDE.md mandate)
variable "region" {
  description = "GCP region (unused by the dashboard resource but retained for stack-wide composer mapping)"
  type        = string
  default     = "us-central1"
}

variable "labels" {
  description = "Additional labels merged into the project label on the notification channels (issue #204)."
  type        = map(string)
  default     = {}
}

variable "notification_channel_emails" {
  description = "Email addresses to register as Cloud Monitoring notification channels. Empty list disables channel creation. The notification_channels output is consumed by per-component alert policies in gcp/<module>/observability.tf (issue #204)."
  type        = list(string)
  default     = []
  validation {
    condition     = alltrue([for e in var.notification_channel_emails : can(regex("^[^@\\s]+@[^@\\s]+\\.[^@\\s]+$", e))])
    error_message = "notification_channel_emails entries must be valid email addresses."
  }
}

variable "dashboard_json" {
  description = "Dashboard JSON spec. The default is a minimal-but-Monitoring-API-valid dashboard with one widget that satisfies the API's 'must contain at least one widget' check; override with your real dashboard JSON."
  type        = string
  default     = <<-EOT
    {
      "displayName": "Main Dashboard",
      "gridLayout": {
        "columns": "2",
        "widgets": [
          {
            "title": "VM CPU Utilization",
            "xyChart": {
              "dataSets": [
                {
                  "timeSeriesQuery": {
                    "timeSeriesFilter": {
                      "filter": "metric.type=\"compute.googleapis.com/instance/cpu/utilization\" resource.type=\"gce_instance\"",
                      "aggregation": {
                        "alignmentPeriod": "60s",
                        "perSeriesAligner": "ALIGN_MEAN",
                        "crossSeriesReducer": "REDUCE_MEAN"
                      }
                    }
                  },
                  "plotType": "LINE"
                }
              ],
              "yAxis": {
                "label": "y1Axis",
                "scale": "LINEAR"
              }
            }
          }
        ]
      }
    }
  EOT
}

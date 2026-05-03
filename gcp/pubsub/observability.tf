# observability.tf — issue #204

variable "enable_observability" {
  description = "When true, emit per-component Cloud Monitoring alert policies (issue #204)."
  type        = bool
  default     = true
}

variable "notification_channels" {
  description = "Cloud Monitoring notification channel names."
  type        = list(string)
  default     = []
}

variable "alarm_severity" {
  description = "Severity tag attached to alert policies."
  type        = string
  default     = "warning"
  validation {
    condition     = contains(["critical", "warning", "info"], var.alarm_severity)
    error_message = "alarm_severity must be one of critical|warning|info."
  }
}

variable "alarm_threshold_overrides" {
  description = "Per-metric numeric threshold overrides."
  type        = map(number)
  default     = {}
}

variable "runbook_url_prefix" {
  description = "Reserved for future runbook content support — currently a no-op. The documentation{} block was removed to fix #240 (empty content rejected by GCP Monitoring API with 400). Will be re-introduced with structured content in a follow-up once the runbook URL convention is settled."
  type        = string
  default     = ""
}

locals {
  _obs_user_labels = { project = var.project, severity = var.alarm_severity }
  _obs_thresholds = merge({
    backlog_messages = 1000
  }, var.alarm_threshold_overrides)
}

resource "google_monitoring_alert_policy" "backlog_high" {
  for_each = var.enable_observability ? { "0" = true } : {}

  project      = var.project_id
  display_name = "${var.project}-pubsub-backlog"
  combiner     = "OR"

  conditions {
    display_name = "Pub/Sub subscription backlog above ${local._obs_thresholds["backlog_messages"]} messages"
    condition_threshold {
      filter          = "metric.type=\"pubsub.googleapis.com/subscription/num_undelivered_messages\" AND resource.type=\"pubsub_subscription\""
      duration        = "300s"
      comparison      = "COMPARISON_GT"
      threshold_value = local._obs_thresholds["backlog_messages"]
      aggregations {
        alignment_period   = "60s"
        per_series_aligner = "ALIGN_MEAN"
      }
    }
  }

  notification_channels = var.notification_channels
  user_labels           = local._obs_user_labels
}

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

locals {
  _obs_user_labels = { project = var.project, severity = var.alarm_severity }
  _obs_thresholds = merge({
    backend_latency_p99_ms = 1000
  }, var.alarm_threshold_overrides)
}

resource "google_monitoring_alert_policy" "backend_latency_high" {
  for_each = var.enable_observability ? { "0" = true } : {}

  project      = var.project_id
  display_name = "${var.project}-lb-backend-latency"
  combiner     = "OR"

  conditions {
    display_name = "HTTPS LB p99 backend latency above ${local._obs_thresholds["backend_latency_p99_ms"]}ms"
    condition_threshold {
      filter          = "metric.type=\"loadbalancing.googleapis.com/https/backend_latencies\" AND resource.type=\"https_lb_rule\""
      duration        = "300s"
      comparison      = "COMPARISON_GT"
      threshold_value = local._obs_thresholds["backend_latency_p99_ms"]
      aggregations {
        alignment_period   = "60s"
        per_series_aligner = "ALIGN_PERCENTILE_99"
      }
    }
  }

  notification_channels = var.notification_channels
  user_labels           = local._obs_user_labels
}

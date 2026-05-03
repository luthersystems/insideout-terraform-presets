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
    request_latency_p99_ms = 2000
  }, var.alarm_threshold_overrides)
}

resource "google_monitoring_alert_policy" "latency_high" {
  for_each = var.enable_observability ? { "0" = true } : {}

  project      = var.project_id
  display_name = "${var.project}-cloud-run-latency"
  combiner     = "OR"

  conditions {
    display_name = "Cloud Run p99 latency above ${local._obs_thresholds["request_latency_p99_ms"]}ms"
    condition_threshold {
      filter          = "metric.type=\"run.googleapis.com/request_latencies\" AND resource.type=\"cloud_run_revision\""
      duration        = "300s"
      comparison      = "COMPARISON_GT"
      threshold_value = local._obs_thresholds["request_latency_p99_ms"]
      aggregations {
        alignment_period   = "60s"
        per_series_aligner = "ALIGN_PERCENTILE_99"
      }
    }
  }

  notification_channels = var.notification_channels
  user_labels           = local._obs_user_labels
}

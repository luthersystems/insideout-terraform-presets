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
  # error_rate is misleading — the underlying metric is execution_count
  # filtered by status!=ok and ALIGN_RATEd, so units are
  # errors-per-second, not a percentage. Default 0.05 means "alert if
  # the function has sustained ≥1 error every 20s." A true ratio
  # (errors/total) would require a paired-metric alert; tracked as
  # follow-up.
  _obs_thresholds = merge({
    error_rate_per_second = 0.05
  }, var.alarm_threshold_overrides)
}

# Gen1 Cloud Functions execution status counter; Gen2 functions are
# backed by Cloud Run and use that module's alarms.
resource "google_monitoring_alert_policy" "errors_high" {
  for_each = var.enable_observability ? { "0" = true } : {}

  project      = var.project_id
  display_name = "${var.project}-cloud-functions-errors"
  combiner     = "OR"

  conditions {
    display_name = "Cloud Functions errors above ${local._obs_thresholds["error_rate_per_second"]} per second"
    condition_threshold {
      filter          = "metric.type=\"cloudfunctions.googleapis.com/function/execution_count\" AND resource.type=\"cloud_function\" AND metric.label.\"status\"!=\"ok\""
      duration        = "300s"
      comparison      = "COMPARISON_GT"
      threshold_value = local._obs_thresholds["error_rate_per_second"]
      aggregations {
        alignment_period   = "60s"
        per_series_aligner = "ALIGN_RATE"
      }
    }
  }

  notification_channels = var.notification_channels
  user_labels           = local._obs_user_labels
}

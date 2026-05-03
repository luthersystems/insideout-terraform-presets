# observability.tf — issue #204
#
# Co-located alert authoring for the Memorystore Redis instance this
# module owns. Net-new — GCP had zero alert policies in the legacy
# layout. The composer feeds notification_channels from the
# gcp_cloud_monitoring aggregator (post-switch wiring loop in
# pkg/composer/contracts.go). When notification_channels is empty
# (no aggregator selected, or aggregator has no email channels), the
# policy still creates but routes nowhere — safe initial-deploy.

variable "enable_observability" {
  description = "When true, emit per-component Cloud Monitoring alert policies gated on this module's resources (issue #204)."
  type        = bool
  default     = true
}

variable "notification_channels" {
  description = "Cloud Monitoring notification channel names (projects/<id>/notificationChannels/<id>) to wire into alert policies. Wired from gcp_cloud_monitoring.notification_channels by the composer."
  type        = list(string)
  default     = []
}

variable "alarm_severity" {
  description = "Severity tag attached to alert policies. One of critical|warning|info."
  type        = string
  default     = "warning"
  validation {
    condition     = contains(["critical", "warning", "info"], var.alarm_severity)
    error_message = "alarm_severity must be one of critical|warning|info."
  }
}

variable "alarm_threshold_overrides" {
  description = "Per-metric numeric threshold overrides; missing keys fall through to the module's defaults."
  type        = map(number)
  default     = {}
}

locals {
  _obs_user_labels = { project = var.project, severity = var.alarm_severity }
  _obs_thresholds = merge({
    cpu_high_pct = 0.8
  }, var.alarm_threshold_overrides)
}

resource "google_monitoring_alert_policy" "cpu_high" {
  for_each = var.enable_observability ? { "0" = true } : {}

  project      = var.project_id
  display_name = "${var.project}-${var.name}-redis-cpu"
  combiner     = "OR"

  conditions {
    display_name = "Redis CPU above ${local._obs_thresholds["cpu_high_pct"] * 100}%"
    condition_threshold {
      filter          = "metric.type=\"redis.googleapis.com/stats/cpu_utilization\" AND resource.type=\"redis_instance\""
      duration        = "300s"
      comparison      = "COMPARISON_GT"
      threshold_value = local._obs_thresholds["cpu_high_pct"]

      aggregations {
        alignment_period   = "60s"
        per_series_aligner = "ALIGN_MEAN"
      }
    }
  }

  notification_channels = var.notification_channels
  user_labels           = local._obs_user_labels
}

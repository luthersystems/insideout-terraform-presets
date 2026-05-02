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
  description = "Optional runbook URL prefix."
  type        = string
  default     = ""
}

locals {
  _obs_user_labels = { project = var.project, severity = var.alarm_severity }
  _obs_thresholds = merge({
    error_rate_5xx = 0.05
  }, var.alarm_threshold_overrides)
  _obs_runbook = var.runbook_url_prefix == "" ? "" : "Runbook: ${var.runbook_url_prefix}/api_gateway/errors"
}

resource "google_monitoring_alert_policy" "errors_high" {
  for_each = var.enable_observability ? { "0" = true } : {}

  project      = var.project_id
  display_name = "${var.project}-api-gateway-errors"
  combiner     = "OR"

  conditions {
    display_name = "API Gateway 5xx error rate above ${local._obs_thresholds["error_rate_5xx"] * 100}%"
    condition_threshold {
      # GCP API Gateway publishes apigateway.googleapis.com/gateway/* under
      # resource.type apigateway.googleapis.com/Gateway. The api/* names and
      # /Api resource type are NOT real (would silently never match). Catalog
      # in pkg/observability/component_observability.go:338-339 carries the
      # canonical names; alarmedGCPMetrics[KeyGCPAPIGateway] gates drift.
      filter          = "metric.type=\"apigateway.googleapis.com/gateway/request_count\" AND resource.type=\"apigateway.googleapis.com/Gateway\" AND metric.label.\"response_code_class\"=\"5xx\""
      duration        = "300s"
      comparison      = "COMPARISON_GT"
      threshold_value = local._obs_thresholds["error_rate_5xx"]
      aggregations {
        alignment_period   = "60s"
        per_series_aligner = "ALIGN_RATE"
      }
    }
  }

  notification_channels = var.notification_channels
  user_labels           = local._obs_user_labels

  documentation {
    content   = local._obs_runbook
    mime_type = "text/markdown"
  }
}

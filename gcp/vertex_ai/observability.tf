# observability.tf — issue #204 / #764
#
# Per-component Cloud Monitoring alert policies for Vertex AI Vector Search.
# Emitted only when Vector Search is enabled (the bare dataset has no serving
# surface to alarm on). Wired the SNS-equivalent inputs (notification_channels,
# enable_observability) from gcp/cloud_monitoring when that aggregator is
# selected — see contracts.go observability post-switch wiring.

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
    query_latency_p99_ms      = 500
    prediction_latency_p99_ms = 2000
    prediction_error_rate     = 1
  }, var.alarm_threshold_overrides)

  # Alarms only make sense when Vector Search is serving queries.
  _obs_enabled = var.enable_observability && var.enable_vector_search

  # Serving alarms (prediction latency / errors) only make sense when a serving
  # endpoint exists. Gated on enable_serving, independent of Vector Search.
  _obs_serving_enabled = var.enable_observability && var.enable_serving
}

resource "google_monitoring_alert_policy" "vector_search_query_latency_high" {
  for_each = local._obs_enabled ? { "0" = true } : {}

  project      = var.project_id
  display_name = "${var.project}-vertex-vector-query-latency"
  combiner     = "OR"

  conditions {
    display_name = "Vector Search p99 query latency above ${local._obs_thresholds["query_latency_p99_ms"]}ms"
    condition_threshold {
      # Public metric on the Matching Engine index endpoint serving surface.
      # Metric path + monitored resource verified against Google's official
      # list (cloud.google.com/monitoring/api/metrics_gcp_a_b#gcp-aiplatform):
      # the metric is matching_engine/query/latencies (slashes, not an
      # underscore) reported on aiplatform.googleapis.com/IndexEndpoint
      # (NOT MatchingEngineIndexEndpoint, which is not a real resource type).
      filter          = "metric.type=\"aiplatform.googleapis.com/matching_engine/query/latencies\" AND resource.type=\"aiplatform.googleapis.com/IndexEndpoint\""
      duration        = "300s"
      comparison      = "COMPARISON_GT"
      threshold_value = local._obs_thresholds["query_latency_p99_ms"]
      aggregations {
        alignment_period   = "60s"
        per_series_aligner = "ALIGN_PERCENTILE_99"
      }
    }
  }

  notification_channels = var.notification_channels
  user_labels           = local._obs_user_labels
}

# --- Serving alarms (#768) --------------------------------------------------
# Online-prediction latency + error alarms on the serving endpoint. Emitted
# only when enable_serving (and observability) are on; the bare dataset / a
# Vector-Search-only stack has no online-prediction surface to alarm on.
#
# Metric paths + monitored resource verified against Google's official metrics
# list (cloud.google.com/monitoring/api/metrics_gcp_a_b#gcp-aiplatform) and the
# pkg/observability `vertexai` catalog (registered in alarmedGCPMetrics so the
# inspector flips Alarmed=true): prediction/online/prediction_latencies and
# prediction/online/error_count, both reported on
# aiplatform.googleapis.com/Endpoint.

resource "google_monitoring_alert_policy" "serving_prediction_latency_high" {
  for_each = local._obs_serving_enabled ? { "0" = true } : {}

  project      = var.project_id
  display_name = "${var.project}-vertex-prediction-latency"
  combiner     = "OR"

  conditions {
    display_name = "Online prediction p99 latency above ${local._obs_thresholds["prediction_latency_p99_ms"]}ms"
    condition_threshold {
      filter          = "metric.type=\"aiplatform.googleapis.com/prediction/online/prediction_latencies\" AND resource.type=\"aiplatform.googleapis.com/Endpoint\""
      duration        = "300s"
      comparison      = "COMPARISON_GT"
      threshold_value = local._obs_thresholds["prediction_latency_p99_ms"]
      aggregations {
        alignment_period   = "60s"
        per_series_aligner = "ALIGN_PERCENTILE_99"
      }
    }
  }

  notification_channels = var.notification_channels
  user_labels           = local._obs_user_labels
}

resource "google_monitoring_alert_policy" "serving_prediction_errors_high" {
  for_each = local._obs_serving_enabled ? { "0" = true } : {}

  project      = var.project_id
  display_name = "${var.project}-vertex-prediction-errors"
  combiner     = "OR"

  conditions {
    display_name = "Online prediction error rate above ${local._obs_thresholds["prediction_error_rate"]}/s"
    condition_threshold {
      filter          = "metric.type=\"aiplatform.googleapis.com/prediction/online/error_count\" AND resource.type=\"aiplatform.googleapis.com/Endpoint\""
      duration        = "300s"
      comparison      = "COMPARISON_GT"
      threshold_value = local._obs_thresholds["prediction_error_rate"]
      aggregations {
        alignment_period   = "60s"
        per_series_aligner = "ALIGN_RATE"
      }
    }
  }

  notification_channels = var.notification_channels
  user_labels           = local._obs_user_labels
}

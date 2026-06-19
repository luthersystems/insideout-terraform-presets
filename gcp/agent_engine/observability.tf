# observability.tf — issue #204 / #769
#
# Declares the standard per-component observability wiring surface
# (enable_observability + notification_channels + severity/threshold knobs) so
# the contract matches the other GCP presets and any future Cloud Monitoring
# wiring binds cleanly.
#
# NO alert policy is emitted yet. Vertex AI Agent Engine (Reasoning Engine) has
# no catalog-registered Cloud Monitoring metric in this repo — emitting a
# google_monitoring_alert_policy against an unverified metric type would page on
# a filter that never matches (or worse, silently never fires). Per the #769
# guard "no alarms unless catalog-registered", this stays var-only until a
# verified Reasoning Engine serving metric is added to the catalog. The preset
# is therefore intentionally NOT listed in PricingDependencies[gcp_cloud_
# monitoring] (no emitter to wire), mirroring gcp/vertex_ai's dataset-only path.

variable "enable_observability" {
  description = "When true, emit per-component Cloud Monitoring alert policies (issue #204). No-op today — the Reasoning Engine has no catalog-registered metric yet."
  type        = bool
  default     = true
}

variable "notification_channels" {
  description = "Cloud Monitoring notification channel names alert policies would page. Unused until a Reasoning Engine alarm is catalog-registered."
  type        = list(string)
  default     = []
}

variable "alarm_severity" {
  description = "Severity tag that would be attached to alert policies."
  type        = string
  default     = "warning"

  validation {
    condition     = contains(["critical", "warning", "info"], var.alarm_severity)
    error_message = "alarm_severity must be one of critical|warning|info."
  }
}

variable "alarm_threshold_overrides" {
  description = "Per-metric numeric threshold overrides for future alert policies."
  type        = map(number)
  default     = {}
}

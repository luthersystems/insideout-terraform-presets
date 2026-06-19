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

# tflint-ignore: terraform_unused_declarations  # contract-surface var (issue #204): no alert policy is emitted yet (the Reasoning Engine has no catalog-registered Cloud Monitoring metric in this repo — see header), so the module body does not reference it. Declared for parity with the other GCP presets' observability surface.
variable "enable_observability" {
  description = "When true, emit per-component Cloud Monitoring alert policies (issue #204). No-op today — the Reasoning Engine has no catalog-registered metric yet."
  type        = bool
  default     = true
}

# tflint-ignore: terraform_unused_declarations  # contract-surface var (issue #204): unused until a Reasoning Engine alarm is catalog-registered and an alert policy references these channels.
variable "notification_channels" {
  description = "Cloud Monitoring notification channel names alert policies would page. Unused until a Reasoning Engine alarm is catalog-registered."
  type        = list(string)
  default     = []
}

# tflint-ignore: terraform_unused_declarations  # contract-surface var (issue #204): the severity tag is attached to alert policies, of which none are emitted yet; the validation still guards the input shape.
variable "alarm_severity" {
  description = "Severity tag that would be attached to alert policies."
  type        = string
  default     = "warning"

  validation {
    condition     = contains(["critical", "warning", "info"], var.alarm_severity)
    error_message = "alarm_severity must be one of critical|warning|info."
  }
}

# tflint-ignore: terraform_unused_declarations  # contract-surface var (issue #204): per-metric threshold overrides for the future alert policies described in the header; unreferenced until they land.
variable "alarm_threshold_overrides" {
  description = "Per-metric numeric threshold overrides for future alert policies."
  type        = map(number)
  default     = {}
}

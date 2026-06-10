# observability.tf — issue #761 (real-time inference endpoint alarms).
#
# Alarms only exist when var.enable_inference is true: with no endpoint there
# are no AWS/SageMaker invocation metrics to alarm on. Each alarm is keyed by
# the EndpointName + VariantName dimensions the endpoint publishes under.
#
# Metric names verified against the AWS/SageMaker endpoint-invocation metric
# table (Invocation5XXErrors, ModelLatency) — not memory (the #764 metric-name
# regression). ModelLatency is reported in MICROSECONDS, which the latency
# threshold default reflects.

variable "enable_observability" {
  description = "When true, emit per-component CloudWatch alarms gated on this module's inference endpoint (issue #761). Has no effect unless var.enable_inference is also true (no endpoint = no metrics to alarm on)."
  type        = bool
  default     = true
}

variable "alarm_topic_arn" {
  description = "SNS topic ARN that receives alarm + ok notifications. When null, the alarm exists but does not notify."
  type        = string
  default     = null
}

variable "alarm_severity" {
  description = "Severity tag attached to alarms. One of critical|warning|info."
  type        = string
  default     = "warning"
  validation {
    condition     = contains(["critical", "warning", "info"], var.alarm_severity)
    error_message = "alarm_severity must be one of critical|warning|info."
  }
}

variable "alarm_threshold_overrides" {
  description = "Per-metric numeric threshold overrides; missing keys fall through to the module's defaults. Keys: invocation_5xx_errors (count per period), model_latency_micros (microseconds)."
  type        = map(number)
  default     = {}
}

variable "runbook_url_prefix" {
  description = "Optional URL prefix included in alarm_description so on-call has a click-through."
  type        = string
  default     = ""
}

locals {
  # Alarms exist only when both the endpoint is provisioned AND observability
  # is enabled. for_each over a 1/0-element map so the alarms read as a single
  # resource that's present or absent.
  _obs_enabled = local.enable_inference && var.enable_observability
  _obs_actions = var.alarm_topic_arn == null ? [] : [var.alarm_topic_arn]
  _obs_tags    = merge(module.name.tags, var.tags, { severity = var.alarm_severity })
  _obs_thresholds = merge({
    invocation_5xx_errors = 1
    # ModelLatency is published in MICROSECONDS. 5_000_000 µs = 5s — a
    # conservative default for an LLM serving endpoint; tune per workload via
    # alarm_threshold_overrides.
    model_latency_micros = 5000000
  }, var.alarm_threshold_overrides)
  _obs_runbook = var.runbook_url_prefix == "" ? "" : " Runbook: ${var.runbook_url_prefix}/sagemaker/inference"

  # Dimension shared by both alarms — the endpoint + its single "primary"
  # production variant (the variant_name set in the endpoint configuration).
  _obs_dimensions = local._obs_enabled ? {
    EndpointName = aws_sagemaker_endpoint.inference[0].name
    VariantName  = "primary"
  } : {}
}

# A spike in Invocation5XXErrors means the hosted model container is returning
# server errors (OOM, crashed worker, bad model artifact) — the inference
# endpoint is up but failing requests. This is the headline failure surface the
# inference slice exists to serve.
resource "aws_cloudwatch_metric_alarm" "invocation_5xx_high" {
  for_each = local._obs_enabled ? { "0" = true } : {}

  alarm_name          = "${module.name.name}-sagemaker-invocation-5xx"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 2
  threshold           = local._obs_thresholds["invocation_5xx_errors"]
  metric_name         = "Invocation5XXErrors"
  namespace           = "AWS/SageMaker"
  period              = 300
  statistic           = "Sum"
  treat_missing_data  = "notBreaching"
  alarm_description   = "SageMaker endpoint 5XX invocation errors above ${local._obs_thresholds["invocation_5xx_errors"]} per period — the hosted model is failing requests.${local._obs_runbook}"
  dimensions          = local._obs_dimensions
  alarm_actions       = local._obs_actions
  ok_actions          = local._obs_actions
  tags                = local._obs_tags
}

# Rising ModelLatency means the container is taking longer to return inferences
# (overloaded instance, cold model, GPU contention). Sustained high latency on
# a real-time endpoint degrades every caller.
resource "aws_cloudwatch_metric_alarm" "model_latency_high" {
  for_each = local._obs_enabled ? { "0" = true } : {}

  alarm_name          = "${module.name.name}-sagemaker-model-latency"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 3
  threshold           = local._obs_thresholds["model_latency_micros"]
  metric_name         = "ModelLatency"
  namespace           = "AWS/SageMaker"
  period              = 300
  statistic           = "Average"
  treat_missing_data  = "notBreaching"
  alarm_description   = "SageMaker endpoint ModelLatency (avg) above ${local._obs_thresholds["model_latency_micros"]}µs — the hosted model is responding slowly.${local._obs_runbook}"
  dimensions          = local._obs_dimensions
  alarm_actions       = local._obs_actions
  ok_actions          = local._obs_actions
  tags                = local._obs_tags
}

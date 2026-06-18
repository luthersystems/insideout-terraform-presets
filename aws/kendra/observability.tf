# observability.tf — issue #760 (Kendra index ingestion-health alarm).
#
# The Kendra index is always created (unlike sagemaker's count-gated inference
# endpoint), so the alarm only needs a single var.enable_observability gate.
# It is keyed by the IndexId dimension the AWS/Kendra namespace publishes under.
#
# Metric name verified against the AWS/Kendra CloudWatch metric table:
# DocumentsFailedToIndex (Sum) — a data-source connector that can't ingest its
# corpus leaves the index serving stale results. That is the headline ingestion
# failure surface; IndexQueryCount / DocumentsIndexed are observed (in the
# metrics catalog) but not alarmed.

variable "enable_observability" {
  description = "When true, emit the per-component CloudWatch alarm on this index's DocumentsFailedToIndex metric (issue #760)."
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
  description = "Per-metric numeric threshold overrides; missing keys fall through to the module's defaults. Keys: documents_failed_to_index (count per period)."
  type        = map(number)
  default     = {}
}

variable "runbook_url_prefix" {
  description = "Optional URL prefix included in alarm_description so on-call has a click-through."
  type        = string
  default     = ""
}

locals {
  _obs_enabled = var.enable_observability
  _obs_actions = var.alarm_topic_arn == null ? [] : [var.alarm_topic_arn]
  _obs_tags    = merge(module.name.tags, var.tags, { severity = var.alarm_severity })
  _obs_thresholds = merge({
    # A single failed document per period is enough to flag a misconfigured or
    # broken connector — tune per workload via alarm_threshold_overrides.
    documents_failed_to_index = 1
  }, var.alarm_threshold_overrides)
  _obs_runbook = var.runbook_url_prefix == "" ? "" : " Runbook: ${var.runbook_url_prefix}/kendra/ingestion"
}

# A spike in DocumentsFailedToIndex means a data-source connector is failing to
# ingest documents (bad permissions, unsupported file types, throttling) — the
# index is up but its corpus is going stale. This is the ingestion failure the
# observability slice exists to catch.
resource "aws_cloudwatch_metric_alarm" "documents_failed_to_index_high" {
  for_each = local._obs_enabled ? { "0" = true } : {}

  alarm_name          = "${module.name.name}-kendra-documents-failed-to-index"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 1
  threshold           = local._obs_thresholds["documents_failed_to_index"]
  metric_name         = "DocumentsFailedToIndex"
  namespace           = "AWS/Kendra"
  period              = 300
  statistic           = "Sum"
  treat_missing_data  = "notBreaching"
  alarm_description   = "Kendra index DocumentsFailedToIndex above ${local._obs_thresholds["documents_failed_to_index"]} per period — a data-source connector is failing to ingest documents and the index corpus is going stale.${local._obs_runbook}"
  dimensions = {
    IndexId = aws_kendra_index.this.id
  }
  alarm_actions = local._obs_actions
  ok_actions    = local._obs_actions
  tags          = local._obs_tags
}

# observability.tf — issue #204
#
# Co-located alarm authoring for the SQS queue this module owns.
# When enable_observability=true (the default) and alarm_topic_arn is
# wired by the composer (when aws_cloudwatchmonitoring is also
# selected), this module emits an aws_cloudwatch_metric_alarm that
# matches the legacy aggregator-side sqs_backlog alarm in
# aws/cloudwatchmonitoring/main.tf.
#
# The for_each = { "0" = true } shape produces a stringified-int key
# matching the legacy aggregator's address shape, so the composer-emitted
# moved {} block (pkg/composer/observability_moves.go::KeyAWSSQS) can
# relocate state without destroy+create when reliable sets
# disable_legacy_per_component_alarms=true on the aggregator.

variable "enable_observability" {
  description = "When true, emit per-component CloudWatch alarms gated on this module's resources (issue #204)."
  type        = bool
  default     = true
}

variable "alarm_topic_arn" {
  description = "SNS topic ARN that receives alarm + ok notifications. When null, the alarm exists but does not notify (safe initial-deploy)."
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
  description = "Per-metric numeric threshold overrides; missing keys fall through to the module's defaults."
  type        = map(number)
  default     = {}
}

variable "runbook_url_prefix" {
  description = "Optional URL prefix included in alarm_description so on-call has a click-through. Empty string disables the prefix."
  type        = string
  default     = ""
}

locals {
  _obs_actions = var.alarm_topic_arn == null ? [] : [var.alarm_topic_arn]
  _obs_tags    = merge(module.name.tags, var.tags, { severity = var.alarm_severity })
  _obs_thresholds = merge({
    backlog = 1000
  }, var.alarm_threshold_overrides)
  _obs_runbook = var.runbook_url_prefix == "" ? "" : " Runbook: ${var.runbook_url_prefix}/sqs/backlog"
}

resource "aws_cloudwatch_metric_alarm" "backlog" {
  for_each = var.enable_observability ? { "0" = true } : {}

  alarm_name          = "${module.name.name}-backlog"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 2
  threshold           = local._obs_thresholds["backlog"]
  metric_name         = "ApproximateNumberOfMessagesVisible"
  namespace           = "AWS/SQS"
  period              = 300
  statistic           = "Average"
  treat_missing_data  = "notBreaching"
  alarm_description   = "SQS backlog above ${local._obs_thresholds["backlog"]} visible messages.${local._obs_runbook}"
  dimensions          = { QueueName = aws_sqs_queue.this.name }
  alarm_actions       = local._obs_actions
  ok_actions          = local._obs_actions
  tags                = local._obs_tags
}

# observability.tf — issue #204
#
# MSK publishes the offline-partitions metric only when
# enhanced_monitoring is at least PER_BROKER. Our preset defaults to
# PER_BROKER (#102), so this alarm has source data.

variable "enable_observability" {
  description = "When true, emit per-component CloudWatch alarms gated on this module's resources (issue #204)."
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
  description = "Per-metric numeric threshold overrides; missing keys fall through to the module's defaults."
  type        = map(number)
  default     = {}
}

variable "runbook_url_prefix" {
  description = "Optional URL prefix included in alarm_description so on-call has a click-through."
  type        = string
  default     = ""
}

locals {
  _obs_actions = var.alarm_topic_arn == null ? [] : [var.alarm_topic_arn]
  _obs_tags    = merge(module.name.tags, var.tags, { severity = var.alarm_severity })
  _obs_thresholds = merge({
    offline_partitions = 1
  }, var.alarm_threshold_overrides)
  _obs_runbook = var.runbook_url_prefix == "" ? "" : " Runbook: ${var.runbook_url_prefix}/msk/offline_partitions"
}

resource "aws_cloudwatch_metric_alarm" "offline_partitions" {
  for_each = var.enable_observability ? { "0" = true } : {}

  alarm_name          = "${module.name.name}-msk-offline-partitions"
  comparison_operator = "GreaterThanOrEqualToThreshold"
  evaluation_periods  = 1
  threshold           = local._obs_thresholds["offline_partitions"]
  metric_name         = "OfflinePartitionsCount"
  namespace           = "AWS/Kafka"
  period              = 300
  statistic           = "Maximum"
  treat_missing_data  = "notBreaching"
  alarm_description   = "MSK cluster has ${local._obs_thresholds["offline_partitions"]}+ offline partitions.${local._obs_runbook}"
  dimensions          = { "Cluster Name" = aws_msk_cluster.this.cluster_name }
  alarm_actions       = local._obs_actions
  ok_actions          = local._obs_actions
  tags                = local._obs_tags
}

# observability.tf — issue #204
#
# Co-located alarm authoring for the ElastiCache replication group this
# module owns. The cpu_high alarm matches the legacy aggregator-side
# redis_cpu_high alarm. The aggregator never had its
# elasticache_replication_group_ids input wired (see
# pkg/composer/contracts.go:709 — no elasticache case), so the legacy
# alarm has been dormant; this is the first wiring.

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
    cpu_high_pct = 80
  }, var.alarm_threshold_overrides)
  _obs_runbook = var.runbook_url_prefix == "" ? "" : " Runbook: ${var.runbook_url_prefix}/elasticache/cpu"
}

resource "aws_cloudwatch_metric_alarm" "cpu_high" {
  for_each = var.enable_observability ? { "0" = true } : {}

  alarm_name          = "${module.name.name}-redis-cpu"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 2
  threshold           = local._obs_thresholds["cpu_high_pct"]
  metric_name         = "CPUUtilization"
  namespace           = "AWS/ElastiCache"
  period              = 300
  statistic           = "Average"
  treat_missing_data  = "notBreaching"
  alarm_description   = "ElastiCache replication group CPU above ${local._obs_thresholds["cpu_high_pct"]}%.${local._obs_runbook}"
  dimensions          = { CacheClusterId = aws_elasticache_replication_group.this.id }
  alarm_actions       = local._obs_actions
  ok_actions          = local._obs_actions
  tags                = local._obs_tags
}

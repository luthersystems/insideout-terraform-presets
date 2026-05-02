# observability.tf — issue #204
#
# Co-located alarm authoring for the ALB this module owns. Currently one
# alarm: elb_5xx_high (target 5xx response count). Net-new — the legacy
# aggregator dashboards ALB metrics but never alarmed on them (audit gap
# from docs/observability-consolidation.md). A target-response-time
# alarm (TargetResponseTime, p95) is tracked as a follow-up — would
# need careful threshold tuning per-stack since target response time is
# heavily workload-dependent.

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
    elb_5xx_count = 50
  }, var.alarm_threshold_overrides)
  _obs_runbook = var.runbook_url_prefix == "" ? "" : " Runbook: ${var.runbook_url_prefix}/alb/"
}

resource "aws_cloudwatch_metric_alarm" "elb_5xx_high" {
  for_each = var.enable_observability ? { "0" = true } : {}

  alarm_name          = "${module.name.name}-alb-5xx"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 2
  threshold           = local._obs_thresholds["elb_5xx_count"]
  metric_name         = "HTTPCode_ELB_5XX_Count"
  namespace           = "AWS/ApplicationELB"
  period              = 300
  statistic           = "Sum"
  treat_missing_data  = "notBreaching"
  alarm_description   = "ALB 5xx errors above ${local._obs_thresholds["elb_5xx_count"]} per period.${local._obs_runbook}5xx"
  dimensions          = { LoadBalancer = aws_lb.alb.arn_suffix }
  alarm_actions       = local._obs_actions
  ok_actions          = local._obs_actions
  tags                = local._obs_tags
}

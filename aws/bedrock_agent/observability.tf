# observability.tf — issue #204

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
    invocation_client_errors = 5
  }, var.alarm_threshold_overrides)
  _obs_runbook = var.runbook_url_prefix == "" ? "" : " Runbook: ${var.runbook_url_prefix}/bedrock_agent/errors"
}

# Bedrock Agents publish InvocationClientErrors under AWS/Bedrock keyed by the
# agent id. A spike means callers are sending malformed InvokeAgent requests or
# the agent is rejecting them (bad alias / unprepared version / missing
# permissions) — exactly the failure surface this preset exists to prevent.
resource "aws_cloudwatch_metric_alarm" "client_errors_high" {
  for_each = var.enable_observability ? { "0" = true } : {}

  alarm_name          = "${module.name.name}-bedrock-agent-client-errors"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 2
  threshold           = local._obs_thresholds["invocation_client_errors"]
  metric_name         = "InvocationClientErrors"
  namespace           = "AWS/Bedrock"
  period              = 300
  statistic           = "Sum"
  treat_missing_data  = "notBreaching"
  alarm_description   = "Bedrock Agent client errors above ${local._obs_thresholds["invocation_client_errors"]} per period.${local._obs_runbook}"
  dimensions          = { AgentId = aws_bedrockagent_agent.this.agent_id }
  alarm_actions       = local._obs_actions
  ok_actions          = local._obs_actions
  tags                = local._obs_tags
}

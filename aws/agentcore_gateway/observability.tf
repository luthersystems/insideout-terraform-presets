# observability.tf — issue #763 (AgentCore gateway error-rate alarm).
#
# The gateway is always created (the Lambda target is count-gated, but the
# gateway + role are not), so the alarm only needs a single
# var.enable_observability gate. It is keyed by the Resource dimension the
# AWS/Bedrock-AgentCore namespace publishes under — the value is the gateway
# ARN (aws_bedrockagentcore_gateway.this.gateway_arn), matching AWS's own
# AgentCore alarm example (Name=Resource,Value=<gateway-arn>).
#
# Metric name verified against the AWS/Bedrock-AgentCore CloudWatch surface:
# SystemErrors (Sum) — gateway-side failures the caller can't fix by retrying.
# That is the headline gateway-health signal; Invocations / Throttles /
# UserErrors / Latency are observed (in the metrics catalog) but not alarmed.
#
# MATURITY CAVEAT: AgentCore is a new AWS service surface and its CloudWatch
# metric set is still maturing (see main.tf header). This alarm is deliberately
# conservative — a single SystemErrors-on-the-gateway alarm — and may need
# re-pinning if AWS reshapes the namespace.

variable "enable_observability" {
  description = "When true, emit the per-component CloudWatch alarm on this gateway's SystemErrors metric (issue #763)."
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
  description = "Per-metric numeric threshold overrides; missing keys fall through to the module's defaults. Keys: system_errors (count per period)."
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
    # A single gateway-side system error per period is enough to flag a
    # degraded gateway — tune per workload via alarm_threshold_overrides.
    system_errors = 1
  }, var.alarm_threshold_overrides)
  _obs_runbook = var.runbook_url_prefix == "" ? "" : " Runbook: ${var.runbook_url_prefix}/agentcore/gateway"
}

# A spike in SystemErrors means the gateway itself is failing requests
# (mis-provisioned target, internal AgentCore error) — failures the caller
# can't fix by retrying. This is the gateway-health signal the observability
# slice exists to catch.
resource "aws_cloudwatch_metric_alarm" "system_errors_high" {
  for_each = local._obs_enabled ? { "0" = true } : {}

  alarm_name          = "${module.name.name}-agentcore-gateway-system-errors"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 1
  threshold           = local._obs_thresholds["system_errors"]
  metric_name         = "SystemErrors"
  namespace           = "AWS/Bedrock-AgentCore"
  period              = 300
  statistic           = "Sum"
  treat_missing_data  = "notBreaching"
  alarm_description   = "AgentCore gateway SystemErrors above ${local._obs_thresholds["system_errors"]} per period — the gateway is failing requests it can't recover from by retrying.${local._obs_runbook}"
  dimensions = {
    Resource = aws_bedrockagentcore_gateway.this.gateway_arn
  }
  alarm_actions = local._obs_actions
  ok_actions    = local._obs_actions
  tags          = local._obs_tags
}

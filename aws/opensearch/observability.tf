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
    cluster_red = 1
  }, var.alarm_threshold_overrides)
  _obs_runbook = var.runbook_url_prefix == "" ? "" : " Runbook: ${var.runbook_url_prefix}/opensearch/cluster_red"
}

# Only fires for the managed-OpenSearch deployment type — serverless
# (AOSS) does not publish ClusterStatus metrics. The compound for_each
# gate keeps the destination address shape ["0"] consistent for the
# composer's moved-block contract.
resource "aws_cloudwatch_metric_alarm" "cluster_red" {
  for_each = var.enable_observability && var.deployment_type == "managed" ? { "0" = true } : {}

  alarm_name          = "${module.name.name}-opensearch-cluster-red"
  comparison_operator = "GreaterThanOrEqualToThreshold"
  evaluation_periods  = 1
  threshold           = local._obs_thresholds["cluster_red"]
  metric_name         = "ClusterStatus.red"
  namespace           = "AWS/ES"
  period              = 300
  statistic           = "Maximum"
  treat_missing_data  = "notBreaching"
  alarm_description   = "OpenSearch cluster status reports red.${local._obs_runbook}"
  dimensions          = { DomainName = aws_opensearch_domain.managed[0].domain_name }
  alarm_actions       = local._obs_actions
  ok_actions          = local._obs_actions
  tags                = local._obs_tags
}

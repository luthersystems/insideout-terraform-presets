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
    # Serverless OCU ceilings. AOSS bills per OpenSearch Compute Unit; these
    # alarms fire when sustained search/indexing OCU consumption crosses the
    # threshold so a runaway index build or query storm surfaces as a page
    # rather than a month-end bill. The default of 8 sits well above an
    # idle/baseline collection's OCU usage so the alarm only trips on real
    # scale-out; tune per workload via alarm_threshold_overrides
    # ({ search_ocu = N, indexing_ocu = N }).
    search_ocu   = 8
    indexing_ocu = 8
  }, var.alarm_threshold_overrides)
  _obs_runbook = var.runbook_url_prefix == "" ? "" : " Runbook: ${var.runbook_url_prefix}/opensearch/cluster_red"
  # Per-alarm runbook click-throughs. Each alarm points at its own page so the
  # OCU alarms do not deep-link to the cluster_red runbook (different failure
  # mode, different remediation).
  _obs_runbook_search_ocu   = var.runbook_url_prefix == "" ? "" : " Runbook: ${var.runbook_url_prefix}/opensearch/search_ocu"
  _obs_runbook_indexing_ocu = var.runbook_url_prefix == "" ? "" : " Runbook: ${var.runbook_url_prefix}/opensearch/indexing_ocu"
  # AOSS OCU metrics are published at the ACCOUNT level under the ClientId
  # dimension (= account ID) — there is no per-collection OCU breakdown in
  # CloudWatch (verified against the AWS/AOSS metrics reference, 2026-06).
  # The alarm is therefore account-region-scoped: if several AOSS collections
  # share an account each module instance creates its own alarm watching the
  # same shared metric, which is acceptable (duplicate notifications, not
  # wrong data). The alarm name carries the module/project prefix so the
  # duplicates are distinguishable in the console.
  # try()-wrapped to match the empty-tuple defensive idiom used for the
  # count-conditional SLR data sources in main.tf: data.aws_caller_identity.obs
  # is itself count-conditional, so [0] indexes an empty tuple in managed mode.
  # The ternary already short-circuits, but try() makes the access robust even
  # if a future refactor moves this into a meta-argument (count/for_each)
  # expression where Terraform statically analyses the [0] before the guard.
  _obs_account_id      = local.is_serverless ? try(data.aws_caller_identity.obs[0].account_id, "") : ""
  _obs_serverless_gate = var.enable_observability && local.is_serverless ? { "0" = true } : {}
}

# Account ID for the AOSS OCU alarm ClientId dimension. Only resolved in
# serverless mode (managed mode has no AOSS metrics). Separate from any
# caller-identity data source other resources might add — named .obs to make
# the observability ownership explicit.
data "aws_caller_identity" "obs" {
  count = local.is_serverless ? 1 : 0
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

# --- Serverless (AOSS) OCU alarms ------------------------------------------
#
# Only fire for the serverless deployment type — managed domains do not
# publish AWS/AOSS metrics. SearchOCU and IndexingOCU are the two OpenSearch
# Compute Unit consumption metrics; they are account-level (ClientId
# dimension), so the alarm is account-region-scoped, not per-collection (see
# the local block above). The compound for_each gate keeps the destination
# address shape ["0"] consistent for the composer's moved-block contract,
# mirroring the cluster_red alarm.
resource "aws_cloudwatch_metric_alarm" "search_ocu" {
  for_each = local._obs_serverless_gate

  alarm_name          = "${module.name.name}-aoss-search-ocu"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 3
  threshold           = local._obs_thresholds["search_ocu"]
  metric_name         = "SearchOCU"
  namespace           = "AWS/AOSS"
  period              = 300
  statistic           = "Maximum"
  treat_missing_data  = "notBreaching"
  alarm_description   = "AOSS search OCU consumption is sustained above ${local._obs_thresholds["search_ocu"]} (account-level, ClientId=${local._obs_account_id}).${local._obs_runbook_search_ocu}"
  dimensions          = { ClientId = local._obs_account_id }
  alarm_actions       = local._obs_actions
  ok_actions          = local._obs_actions
  tags                = local._obs_tags
}

resource "aws_cloudwatch_metric_alarm" "indexing_ocu" {
  for_each = local._obs_serverless_gate

  alarm_name          = "${module.name.name}-aoss-indexing-ocu"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = 3
  threshold           = local._obs_thresholds["indexing_ocu"]
  metric_name         = "IndexingOCU"
  namespace           = "AWS/AOSS"
  period              = 300
  statistic           = "Maximum"
  treat_missing_data  = "notBreaching"
  alarm_description   = "AOSS indexing OCU consumption is sustained above ${local._obs_thresholds["indexing_ocu"]} (account-level, ClientId=${local._obs_account_id}).${local._obs_runbook_indexing_ocu}"
  dimensions          = { ClientId = local._obs_account_id }
  alarm_actions       = local._obs_actions
  ok_actions          = local._obs_actions
  tags                = local._obs_tags
}

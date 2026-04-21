mock_provider "aws" {}

# Regression for #95 (phase 2b). Managed-mode OpenSearch domains must
# publish INDEX_SLOW_LOGS, SEARCH_SLOW_LOGS, and ES_APPLICATION_LOGS to
# CloudWatch, or reliable2 panels charting those signals stay on
# "Pending data" no matter how much traffic reaches the domain.
#
# AUDIT_LOGS requires fine-grained access control (advanced_security_options
# with a master user), which this module does not currently enable —
# asserted absent so a future opt-in doesn't ship by surprise.
#
# Serverless mode does not expose stage-level log publishing and must not
# create any of these log-delivery resources.

run "managed_mode_publishes_three_log_types_to_cloudwatch" {
  command = plan

  # The SLR probe is shared between every managed-mode test; force the
  # creation branch so we lock the whole log-publishing chain with the
  # SLR in place.
  override_data {
    target = data.aws_iam_roles.opensearch_slr[0]
    values = {
      names = []
    }
  }

  variables {
    project         = "logs-test"
    region          = "us-east-1"
    environment     = "test"
    deployment_type = "managed"
    vpc_id          = "vpc-12345"
    subnet_ids      = ["subnet-aaa"]
  }

  # --- Log groups: one per supported type, no more, no fewer.
  assert {
    condition     = length(aws_cloudwatch_log_group.opensearch) == 3
    error_message = "Managed mode must create exactly 3 CloudWatch log groups (INDEX_SLOW_LOGS, SEARCH_SLOW_LOGS, ES_APPLICATION_LOGS)."
  }

  assert {
    condition     = contains(keys(aws_cloudwatch_log_group.opensearch), "INDEX_SLOW_LOGS")
    error_message = "INDEX_SLOW_LOGS log group missing — slow-index panels will stay empty."
  }

  assert {
    condition     = contains(keys(aws_cloudwatch_log_group.opensearch), "SEARCH_SLOW_LOGS")
    error_message = "SEARCH_SLOW_LOGS log group missing — slow-search panels will stay empty."
  }

  assert {
    condition     = contains(keys(aws_cloudwatch_log_group.opensearch), "ES_APPLICATION_LOGS")
    error_message = "ES_APPLICATION_LOGS log group missing — application-error panels will stay empty."
  }

  # AUDIT_LOGS is intentionally excluded; assert it so a future addition
  # without the matching advanced_security_options change is caught here.
  assert {
    condition     = !contains(keys(aws_cloudwatch_log_group.opensearch), "AUDIT_LOGS")
    error_message = "AUDIT_LOGS must stay disabled until the module enables fine-grained access control (see #95)."
  }

  # --- Naming: every log group lives under /aws/opensearch/<project>-search/
  # so the resource-policy ARN wildcard keeps matching. Use alltrue/startswith
  # so a typo in the kebab-case transform on any log type is caught, not just
  # whichever key happened to be spot-checked.
  assert {
    condition = alltrue([
      for g in aws_cloudwatch_log_group.opensearch :
      startswith(g.name, "/aws/opensearch/logs-test-search/")
    ])
    error_message = "Every log-group name must sit under /aws/opensearch/<project>-search/ — the resource-policy wildcard matches on this prefix."
  }

  # --- Retention honours the var default on every log group, not just one.
  assert {
    condition     = alltrue([for g in aws_cloudwatch_log_group.opensearch : g.retention_in_days == 30])
    error_message = "Default log_retention_days must be 30 on every managed-mode log group — mismatched retentions would hide slow-log regressions."
  }

  # --- Project tag propagation. reliable3's drift inspector filters on
  # Project=<project> for exact-match; an untagged log group is invisible
  # to drift detection AND to CloudWatch metric cost-attribution. The
  # repo's CLAUDE.md calls this out as a historically-recurring miss
  # (see issue #81 and reliable PR #1027).
  assert {
    condition = alltrue([
      for g in aws_cloudwatch_log_group.opensearch :
      lookup(g.tags, "Project", null) == "logs-test"
    ])
    error_message = "Every OpenSearch log group must carry Project=<project> — reliable3's drift inspector filters on this tag (see CLAUDE.md, issue #81)."
  }

  # --- Resource policy exists.
  assert {
    condition     = length(aws_cloudwatch_log_resource_policy.opensearch_logs) == 1
    error_message = "Managed mode must create one CloudWatch resource policy authorising es.amazonaws.com to write the log groups — the 10-policy-per-account limit means consolidating is worth locking down here."
  }

  # --- Policy Effect must be Allow; a mutation to Deny would silently
  # break log delivery without any config-shape diff.
  assert {
    condition     = jsondecode(aws_cloudwatch_log_resource_policy.opensearch_logs[0].policy_document).Statement[0].Effect == "Allow"
    error_message = "Resource policy statement Effect must be Allow."
  }

  # --- Policy Principal grants only OpenSearch — any other identifier
  # here leaks authority to write to slow/application logs.
  assert {
    condition     = jsondecode(aws_cloudwatch_log_resource_policy.opensearch_logs[0].policy_document).Statement[0].Principal.Service == "es.amazonaws.com"
    error_message = "Resource policy must grant es.amazonaws.com as the Service principal — otherwise OpenSearch cannot PutLogEvents."
  }

  # --- Policy Action is EXACTLY PutLogEvents + CreateLogStream. Length
  # check catches widening (e.g. adding logs:*) and contains() catches
  # narrowing. Together they pin the set both directions.
  assert {
    condition     = length(jsondecode(aws_cloudwatch_log_resource_policy.opensearch_logs[0].policy_document).Statement[0].Action) == 2
    error_message = "Resource policy Action must have exactly 2 entries — widening (e.g. logs:*) would leak authority to es.amazonaws.com."
  }

  assert {
    condition     = contains(jsondecode(aws_cloudwatch_log_resource_policy.opensearch_logs[0].policy_document).Statement[0].Action, "logs:PutLogEvents")
    error_message = "Resource policy must allow logs:PutLogEvents; dropping it means the domain create succeeds but logs never land."
  }

  assert {
    condition     = contains(jsondecode(aws_cloudwatch_log_resource_policy.opensearch_logs[0].policy_document).Statement[0].Action, "logs:CreateLogStream")
    error_message = "Resource policy must allow logs:CreateLogStream; OpenSearch creates a per-domain log stream on first write and needs this to bootstrap."
  }

  # --- Policy Resource wildcard scope.
  assert {
    condition     = jsondecode(aws_cloudwatch_log_resource_policy.opensearch_logs[0].policy_document).Statement[0].Resource == "arn:aws:logs:*:*:log-group:/aws/opensearch/logs-test-search/*:*"
    error_message = "Resource policy ARN wildcard must stay scoped to the module's log-group prefix — broader scopes leak authority, narrower scopes break log delivery."
  }

  # Log publishing on the domain itself shares the for_each driver
  # (local.opensearch_log_types) with the log-group map above, so the
  # log-group count assertion implicitly locks the publishing-block count.
  # Direct length/enabled assertions on aws_opensearch_domain.managed[0].log_publishing_options
  # are unstable under plan+mock_provider (the nested object values depend
  # on the log-group ARNs which are unknown-at-plan), so we rely on the
  # log-group map assertions above as the proxy.
}

run "serverless_mode_creates_no_log_publishing_resources" {
  command = plan

  override_data {
    target = data.aws_iam_roles.aoss_slr[0]
    values = {
      names = ["AWSServiceRoleForAmazonOpenSearchServerless"]
    }
  }

  variables {
    project         = "logs-test"
    region          = "us-east-1"
    environment     = "test"
    deployment_type = "serverless"
  }

  assert {
    condition     = length(aws_cloudwatch_log_group.opensearch) == 0
    error_message = "Serverless mode must not create managed-domain log groups — AOSS has no domain-level log publishing."
  }

  assert {
    condition     = length(aws_cloudwatch_log_resource_policy.opensearch_logs) == 0
    error_message = "Serverless mode must not create the CloudWatch resource policy — no log groups to scope it to."
  }

  assert {
    condition     = length(aws_opensearch_domain.managed) == 0
    error_message = "Serverless mode must not create a managed OpenSearch domain."
  }
}

run "managed_mode_respects_custom_log_retention" {
  command = plan

  override_data {
    target = data.aws_iam_roles.opensearch_slr[0]
    values = {
      names = ["AWSServiceRoleForAmazonOpenSearchService"]
    }
  }

  variables {
    project            = "logs-test"
    region             = "us-east-1"
    environment        = "test"
    deployment_type    = "managed"
    vpc_id             = "vpc-12345"
    subnet_ids         = ["subnet-aaa"]
    log_retention_days = 7
  }

  assert {
    condition     = alltrue([for g in aws_cloudwatch_log_group.opensearch : g.retention_in_days == 7])
    error_message = "log_retention_days override must propagate to every log group — otherwise slow-log retention silently falls back to the default."
  }
}

#!/usr/bin/env bash
# Static analysis: every taggable AWS resource must carry
#   tags = merge(module.name.tags, var.tags)
# (or an equivalent merge containing module.name.tags) so the Project tag
# emitted by module.name.tags reaches the resource. The downstream reliable3
# inspector filters AWS resources by exact Project = <project> match —
# untagged resources are invisible to drift detection and CloudWatch metrics.
# See issue #81 and https://github.com/luthersystems/reliable/pull/1027.
#
# When this check fails, the fix is almost always:
#   tags = merge(module.name.tags, var.tags)
# Only add a resource type to NON_TAGGABLE_AWS below if the AWS provider
# genuinely doesn't accept a tags block on it (confirmed against the
# hashicorp/aws provider 6.x docs).
#
# Scope: AWS only. GCP labels have too many "not supported" resources to
# enforce cleanly; the /audit skill's Tagging Coverage step covers GCP by
# review.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

# Resource types that do NOT accept a tags attribute in AWS provider 6.x.
# Keep sorted alphabetically.
NON_TAGGABLE_AWS=(
  aws_apigatewayv2_api_mapping
  aws_backup_selection
  aws_bedrock_model_invocation_logging_configuration
  aws_cloudfront_monitoring_subscription
  aws_cloudfront_origin_access_identity
  aws_cloudwatch_dashboard
  aws_cloudwatch_log_resource_policy
  aws_cloudwatch_log_stream
  aws_cognito_identity_provider
  aws_cognito_user_pool_client
  aws_cognito_user_pool_domain
  aws_dynamodb_contributor_insights
  aws_ecs_cluster_capacity_providers
  aws_iam_role_policy
  aws_iam_role_policy_attachment
  aws_iam_service_linked_role
  aws_kms_alias
  aws_msk_configuration
  aws_opensearchserverless_access_policy
  aws_opensearchserverless_security_policy
  aws_s3_bucket_lifecycle_configuration
  aws_s3_bucket_ownership_controls
  aws_s3_bucket_policy
  aws_s3_bucket_public_access_block
  aws_s3_bucket_server_side_encryption_configuration
  aws_s3_bucket_versioning
  aws_security_group_rule
  aws_sns_topic_subscription
  aws_wafv2_web_acl_association
)

skip_pattern="^($(IFS='|'; echo "${NON_TAGGABLE_AWS[*]}"))$"

echo "=== Checking AWS resources for Project tag (tags = merge(module.name.tags, var.tags)) ==="
echo

any_fail=0
for f in "$REPO_ROOT"/aws/*/main.tf; do
  [ -f "$f" ] || continue
  awk -v skip_pattern="$skip_pattern" '
    BEGIN { in_res=0; failed=0 }
    /^resource "aws_/ {
      in_res=1
      res=$2; gsub(/"/, "", res)
      start=NR
      has_tags=0
      has_tag_block=0
      skip_this=(res ~ skip_pattern)
      next
    }
    in_res && /^  tags[[:space:]]*=/ { has_tags=1 }
    in_res && /^  tag[[:space:]]*\{/ { has_tag_block=1 }
    in_res && /^}/ {
      if (!has_tags && !has_tag_block && !skip_this) {
        printf "ERROR: %s:%d: resource %s missing tags = merge(module.name.tags, var.tags)\n", FILENAME, start, res
        failed=1
      }
      in_res=0
    }
    END { exit (failed ? 1 : 0) }
  ' "$f" || any_fail=1
done

if (( any_fail )); then
  echo
  echo "FAIL: One or more taggable AWS resources are missing the Project-tag convention."
  echo "Fix: add  tags = merge(module.name.tags, var.tags)  to each resource."
  echo "If the resource genuinely doesn't accept tags in AWS provider 6.x,"
  echo "add it to NON_TAGGABLE_AWS in tests/lint-project-tag.sh (alphabetically)."
  exit 1
fi

echo "PASS: All taggable AWS resources carry the Project-tag convention."

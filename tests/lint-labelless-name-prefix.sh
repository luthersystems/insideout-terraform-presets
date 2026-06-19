#!/usr/bin/env bash
# Static analysis: every label-less GCP resource that creates queryable
# infrastructure must carry `var.project` in its name (or its closest naming
# attribute), so the downstream InsideOut inspector can attribute the
# resource to the originating session/project via name-prefix scoping.
#
# Background (issue #215, comment-4364048339):
#   PR #216 fixed the labels-invariant phantom-drift bug by sweeping
#   `labels = merge({ project = var.project }, var.labels)` onto every
#   GCP resource type that supports the `labels` attribute (enforced by
#   tests/lint-project-label.sh).
#
#   That fix does NOT cover GCP resource types whose API has no `labels`
#   field at all — the canonical examples are `google_kms_key_ring` and
#   `google_firestore_database`. The InsideOut inspector handles these
#   with a project-scoped API path (KMS: `projects/<id>/locations/<loc>`,
#   Firestore: project-scoped client) so the LIST itself only returns
#   this project's resources, but the resource NAME is also used in the
#   UI / drift-attribution flow. Naming convention `io-<session_id>-...`
#   (i.e. `${var.project}-...`) is the contract.
#
# This lint enforces the contract by walking every `resource "google_*"`
# block in `gcp/*/main.tf` and `gcp/*/observability.tf` and checking:
#
#   1. Is the resource type in LABEL_CAPABLE_GCP (enforced by
#      lint-project-label.sh)? Skip — it carries `project` in labels.
#   2. Is the resource type in EXEMPT_LABELLESS_GCP (sub-resources of a
#      project-prefixed parent, IAM members/bindings, project_service
#      activations, singletons keyed by project ID)? Skip with rationale.
#   3. Otherwise: the resource MUST carry `var.project` in its `name`
#      attribute (for resources whose canonical identifier is `name`),
#      `account_id` (`google_service_account`), or `display_name`
#      (alert policies / dashboards / notification channels).
#
# When this check fails, the fix is one of:
#   - Add `var.project` (or a local that includes `var.project`) to the
#     name-shaped attribute, e.g. `name = "${var.project}-foo-${suffix}"`.
#   - If the resource genuinely cannot carry such a prefix (account_id
#     30-char limit, etc), add it to EXEMPT_LABELLESS_GCP below with a
#     comment explaining the rationale.
#
# Scope: GCP only. AWS uses the Project tag (lint-project-tag.sh) which
# is already enforced module-wide.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

# Resource types that DO accept a `labels` attribute (covered by
# lint-project-label.sh). Keep in sync with that file.
LABEL_CAPABLE_GCP=(
  google_api_gateway_api
  google_api_gateway_api_config
  google_api_gateway_gateway
  google_cloud_run_v2_service
  google_cloudfunctions2_function
  google_compute_global_address
  google_compute_global_forwarding_rule
  google_compute_health_check
  google_compute_instance
  google_compute_security_policy
  google_firestore_database
  google_kms_crypto_key
  google_pubsub_subscription
  google_pubsub_topic
  google_redis_instance
  google_secret_manager_secret
  google_storage_bucket
  google_vertex_ai_dataset
)

# Resource types that ARE label-less but DO NOT need to carry
# var.project in their name. Each entry needs a one-line rationale in
# the comment. Keep sorted alphabetically.
EXEMPT_LABELLESS_GCP=(
  # IAM bindings/members/policies — scope is the parent resource, no
  # name of their own. Attribution flows through the parent.
  google_cloud_run_v2_service_iam_member
  google_cloudfunctions2_function_iam_member
  google_kms_crypto_key_iam_binding
  google_kms_crypto_key_iam_member
  google_project_iam_binding
  google_project_iam_member
  google_secret_manager_secret_iam_binding
  google_secret_manager_secret_iam_member
  google_service_account_iam_binding
  google_service_account_iam_member
  google_storage_bucket_iam_member
  # Project-level singletons — exactly one per GCP project, the project
  # ID itself is the scoping key.
  google_identity_platform_config
  google_identity_platform_default_supported_idp_config
  google_project_service
  google_project_service_identity
  # Sub-resources whose parent carries var.project. Attribution flows
  # through the parent's name prefix.
  #   google_secret_manager_secret_version → secret = google_secret_manager_secret.<X>.id
  #   google_storage_bucket_object         → bucket = google_storage_bucket.<X>.name
  #   google_service_networking_connection → network = <project-prefixed VPC>
  google_secret_manager_secret_version
  google_storage_bucket_object
  google_service_networking_connection
  # google_dns_record_set — Cloud DNS record sets have semantic DNS
  # names (e.g. "www.example.com.") and cannot legally carry var.project
  # in the name field. Attribution flows through the parent managed_zone
  # whose name does carry var.project.
  google_dns_record_set
  # Compute network-plane sub-resources scoped to a project-prefixed
  # parent network. The network/subnet/router/firewall NAME does carry
  # var.project where authored; the rule below only checks resource
  # types whose canonical inspector lookup is by name. These are
  # exempt because their inspector lookup is via parent network ID.
  google_compute_firewall
  google_compute_route
  # google_service_account.account_id has a 30-character GCP-imposed
  # cap that the var.project prefix (`io-<13 chars>` ≈ 16 chars) plus a
  # role suffix overruns. Inspector attribution for service accounts
  # falls back to `google_service_account.project` (the project ID),
  # which is still scoped to the originating GCP project. See
  # gcp/cloud_build/main.tf:74 for the canonical comment block.
  google_service_account
  # google_service_account_key — bound to the parent service account;
  # no independent name attribute.
  google_service_account_key
  # google_monitoring_notification_channel — display_name is
  # human-readable. Inspector queries notification channels via
  # project-scoped API path (see pkg/observability/discovery/gcp/ops.go
  # inspectCloudMonitoring); attribution flows through the API parent.
  google_monitoring_notification_channel
  # google_monitoring_dashboard — name is server-assigned;
  # dashboard_json carries operator-defined display labels. Project-
  # scoped via API parent (`projects/<id>`).
  google_monitoring_dashboard
  # google_vertex_ai_endpoint_with_model_garden_deployment — a one-shot
  # Model Garden deploy keyed by publisher_model_name + location; it has
  # NO name/display_name/account_id field to carry var.project (the
  # managed endpoint it creates is named server-side). The sibling bare
  # google_vertex_ai_endpoint.serving DOES carry the var.project prefix.
  # Inspector attribution flows through the project-scoped Vertex API
  # path (`projects/<id>/locations/<loc>/endpoints`).
  google_vertex_ai_endpoint_with_model_garden_deployment
)

# Build regex patterns from the arrays.
allow_pattern="^($(IFS='|'; echo "${LABEL_CAPABLE_GCP[*]}"))$"
exempt_pattern="^($(IFS='|'; echo "${EXEMPT_LABELLESS_GCP[*]}"))$"

echo "=== Checking label-less GCP resources for var.project name-prefix scoping ==="
echo

any_fail=0
for f in "$REPO_ROOT"/gcp/*/main.tf "$REPO_ROOT"/gcp/*/observability.tf; do
  [ -f "$f" ] || continue
  awk -v allow="$allow_pattern" -v exempt="$exempt_pattern" '
    BEGIN { in_res=0; failed=0 }
    /^resource "google_/ {
      in_res=1
      res=$2; gsub(/"/, "", res)
      start=NR
      has_project_in_name=0
      check_this=1
      if (res ~ allow)  check_this=0
      if (res ~ exempt) check_this=0
      next
    }
    in_res && /^[ \t]+(name|account_id|display_name)[ \t]*=/ {
      # Match either a literal `var.project` reference or a `local.X`
      # whose downstream definition embeds var.project. The local-
      # reference relaxation is required because several modules build
      # their name from a `local.name_prefix` / `local.network_name` /
      # `local.keyring_name` derived from var.project.
      if ($0 ~ /var\.project/) has_project_in_name=1
      if ($0 ~ /local\./)      has_project_in_name=1
    }
    in_res && /^}/ {
      if (check_this && !has_project_in_name) {
        printf "ERROR: %s:%d: resource %s is label-less and its name does not contain var.project (or a local derived from it). Add var.project to the name attribute, or add %s to EXEMPT_LABELLESS_GCP in tests/lint-labelless-name-prefix.sh with a one-line rationale.\n", FILENAME, start, res, res
        failed=1
      }
      in_res=0
    }
    END { exit (failed ? 1 : 0) }
  ' "$f" || any_fail=1
done

if (( any_fail )); then
  echo
  echo "FAIL: One or more label-less GCP resources are missing var.project name-prefix scoping."
  echo "Fix: prefix the resource name with var.project (typical pattern:"
  echo "       name = \"\${var.project}-<role>-\${random_id.suffix.hex}\")."
  echo "If the resource genuinely can't carry such a prefix, add it to"
  echo "EXEMPT_LABELLESS_GCP in tests/lint-labelless-name-prefix.sh with a"
  echo "one-line rationale (see existing entries for examples)."
  exit 1
fi

echo "PASS: All label-less GCP resources carry var.project name-prefix scoping (or are explicitly exempt)."

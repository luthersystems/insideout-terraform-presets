#!/usr/bin/env bash
# Static analysis: every GCP resource that SUPPORTS labels must carry
#   labels = merge({ project = var.project }, var.labels)
# (or an equivalent merge containing the module's project-bearing labels) so
# the Project label emitted by the module reaches the resource. This mirrors
# the AWS Project-tag convention enforced by lint-project-tag.sh — the
# downstream reliable3 inspector filters GCP resources by exact
# project = <project> label match.
#
# Unlike AWS (where most resources accept tags), the majority of GCP
# resources DO NOT accept a labels attribute. This script uses an ALLOWLIST
# of resource types that are known to support `labels` in the Google
# provider v5.x/v6.x. If you add a new GCP resource type and CI flags it as
# not on the allowlist, verify against the provider docs
# (registry.terraform.io/providers/hashicorp/google/latest/docs/resources/<name>):
#   - If the resource supports labels: add the type to LABEL_CAPABLE_GCP
#     below and set labels = merge(...) on the resource.
#   - If the resource does NOT support labels: no action needed; the script
#     ignores it.
#
# Scope: GCP only. See tests/lint-project-tag.sh for the AWS companion.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

# GCP resource types that accept a `labels` attribute in the Google provider
# v5.x/v6.x. Verified against registry.terraform.io provider docs.
# Keep sorted alphabetically.
LABEL_CAPABLE_GCP=(
  google_api_gateway_api
  google_api_gateway_api_config
  google_api_gateway_gateway
  google_cloud_run_v2_service
  google_cloudfunctions2_function
  google_compute_global_address
  google_compute_global_forwarding_rule
  google_compute_instance
  google_compute_security_policy
  google_pubsub_subscription
  google_pubsub_topic
  google_redis_instance
  google_secret_manager_secret
  google_storage_bucket
  google_vertex_ai_dataset
)

allow_pattern="^($(IFS='|'; echo "${LABEL_CAPABLE_GCP[*]}"))$"

echo "=== Checking GCP resources for project label (labels = merge({ project = var.project }, var.labels)) ==="
echo

any_fail=0
for f in "$REPO_ROOT"/gcp/*/main.tf; do
  [ -f "$f" ] || continue
  awk -v allow_pattern="$allow_pattern" '
    BEGIN { in_res=0; failed=0 }
    /^resource "google_/ {
      in_res=1
      res=$2; gsub(/"/, "", res)
      start=NR
      has_labels=0
      check_this=(res ~ allow_pattern)
      next
    }
    in_res && /^  labels[[:space:]]*=/ { has_labels=1 }
    in_res && /^}/ {
      if (check_this && !has_labels) {
        printf "ERROR: %s:%d: resource %s missing labels = merge({ project = var.project }, var.labels)\n", FILENAME, start, res
        failed=1
      }
      in_res=0
    }
    END { exit (failed ? 1 : 0) }
  ' "$f" || any_fail=1
done

if (( any_fail )); then
  echo
  echo "FAIL: One or more label-capable GCP resources are missing the project-label convention."
  echo "Fix: add  labels = merge({ project = var.project }, var.labels)  to each resource."
  echo "If you've added a new label-capable GCP resource type, also add it to"
  echo "LABEL_CAPABLE_GCP in tests/lint-project-label.sh (alphabetically)."
  exit 1
fi

echo "PASS: All label-capable GCP resources carry the project-label convention."

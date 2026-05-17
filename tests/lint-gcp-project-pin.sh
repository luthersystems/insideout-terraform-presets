#!/usr/bin/env bash
# Static analysis: every `resource "google_*"` block in `gcp/**/main.tf`
# (and `observability.tf`) MUST set `project = var.project_id`, and every
# `module "<name>"` block in those files MUST pass
# `project_id = var.project_id`. Resource types whose Google provider
# schema does not accept a `project` attribute are listed in
# EXEMPT_PROJECT_PIN_GCP below with a one-line rationale.
#
# Background (issue #287, umbrella reliable#1328, paired ui-core#153):
#   When a `google_*` resource omits an explicit `project = var.project_id`,
#   the Google Terraform provider falls back to the project_id field in
#   the credential JSON. For service-account-key JSONs the credential's
#   project_id matches the SA's home project, so the fallback is invisible.
#   For Workload Identity Federation / `external_account` JSONs (which
#   ui-core#153 makes routine), the credential JSON's project_id is the
#   WIF *pool* project, not the user's stack project — so resources get
#   silently created in the wrong project, fail with 403, and the stack
#   ends up split-brain. See issue #287 for the prod repro.
#
# Why per-resource, not provider-level:
#   The composer-emitted root provider in pkg/composer/compose.go renders
#   `provider "google" { region = ... default_labels = ... }` with NO
#   `project = ...` argument. Per-resource pinning is therefore the only
#   defense for #287. Provider-level pinning is "fine as belt-and-
#   suspenders, but must not be the only thing" (issue #287 §4).
#
# Skiplist semantics:
#   EXEMPT_PROJECT_PIN_GCP holds resource types where the Google provider
#   genuinely has no `project` attribute because the resource is scoped
#   by a parent reference (e.g., secret, bucket, network, key_ring,
#   crypto_key_id). The parent itself pins var.project_id, so attribution
#   flows through the parent. Adding a type here requires verifying the
#   resource has no `project` field at
#   registry.terraform.io/providers/hashicorp/google/latest/docs/resources/<name>.
#   IAM `_member` / `_binding` / `_policy` variants on project-level
#   resources DO accept `project` and MUST NOT be added — only the
#   resource-on-parent variants (e.g. google_storage_bucket_iam_member)
#   are schema-exempt.
#
# Scope: GCP only. AWS has no `project` analog (AWS scopes by IAM
# session, not an explicit `project` attribute). `data "google_*"` blocks
# are intentionally excluded — public-image lookups (e.g. var.image_project
# for Debian/Ubuntu base images at gcp/bastion/main.tf, gcp/compute/main.tf)
# legitimately reference a different project than var.project_id.
#
# TODO: extend the file glob to gcp/_shared/*/main.tf once the _shared
# bucket carries non-fixture content (currently smoke placeholders only).

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

# NOT EXEMPT — these accept `project` and must pin it. Do not add to the
# skiplist below: google_project_iam_member, google_project_iam_binding,
# google_project_iam_policy, google_kms_key_ring, google_secret_manager_secret,
# google_storage_bucket, google_compute_network, google_compute_subnetwork,
# google_compute_firewall, google_compute_route, google_service_account,
# google_pubsub_topic, google_pubsub_subscription, google_cloud_run_v2_service,
# google_cloud_run_v2_service_iam_member, google_cloudfunctions2_function,
# google_cloudfunctions2_function_iam_member, google_redis_instance,
# google_firestore_database, google_identity_platform_*, google_project_service,
# google_project_service_identity, google_vertex_ai_dataset,
# google_api_gateway_*, google_monitoring_*, etc.
#
# EXEMPT — Google provider rejects `project` on these because they're
# scoped by a parent reference. Keep sorted alphabetically with a
# one-line rationale identifying the parent attribute.
EXEMPT_PROJECT_PIN_GCP=(
  google_kms_crypto_key                 # parent: key_ring (gcp/kms/main.tf:82,103)
  google_kms_crypto_key_iam_binding     # parent: crypto_key_id (gcp/kms/main.tf:126)
  google_secret_manager_secret_version  # parent: secret (gcp/cloud_build, gcp/secretmanager)
  google_service_account_iam_binding    # parent: service_account_id (gcp/github_actions/main.tf, #597)
  google_service_networking_connection  # parent: network (gcp/cloudsql/main.tf:41)
  google_storage_bucket_iam_member      # parent: bucket (gcp/cloud_logging/main.tf:60)
  google_storage_bucket_object          # parent: bucket (gcp/cloud_functions/main.tf:56)
)

exempt_pattern="^($(IFS='|'; echo "${EXEMPT_PROJECT_PIN_GCP[*]}"))$"

echo "=== Checking GCP resources and modules for project = var.project_id pin (#287) ==="
echo

any_fail=0
for f in "$REPO_ROOT"/gcp/*/main.tf "$REPO_ROOT"/gcp/*/observability.tf; do
  [ -f "$f" ] || continue
  awk -v exempt_pattern="$exempt_pattern" '
    BEGIN { mode=""; failed=0 }
    /^resource "google_/ {
      mode="resource"
      res=$2; gsub(/"/, "", res)
      start=NR
      has_pin=0
      exempt=(res ~ exempt_pattern)
      next
    }
    /^module "/ {
      mode="module"
      modname=$2; gsub(/"/, "", modname)
      start=NR
      has_pin=0
      next
    }
    mode=="resource" && /^[[:space:]]*project[[:space:]]*=[[:space:]]*var\.project_id([[:space:]]|$|#)/ {
      has_pin=1
    }
    mode=="module" && /^[[:space:]]*project_id[[:space:]]*=[[:space:]]*var\.project_id([[:space:]]|$|#)/ {
      has_pin=1
    }
    mode!="" && /^}/ {
      if (mode=="resource" && !exempt && !has_pin) {
        printf "ERROR: %s:%d: resource %s missing  project = var.project_id  (issue #287). Add the pin, or if the Google provider does not accept project on this resource type, add %s to EXEMPT_PROJECT_PIN_GCP in tests/lint-gcp-project-pin.sh with a one-line rationale.\n", FILENAME, start, res, res
        failed=1
      }
      if (mode=="module" && !has_pin) {
        printf "ERROR: %s:%d: module %s missing  project_id = var.project_id  (issue #287). Vendored sub-modules must explicitly receive the real GCP project ID.\n", FILENAME, start, modname
        failed=1
      }
      mode=""
    }
    END { exit (failed ? 1 : 0) }
  ' "$f" || any_fail=1
done

if (( any_fail )); then
  echo
  echo "FAIL: One or more GCP resources/modules are missing the project = var.project_id pin (#287)."
  echo "Fix: add  project = var.project_id  inside every google_* resource block, and"
  echo "     project_id = var.project_id  inside every module block."
  echo "If the resource type genuinely does not accept a project attribute (parent-"
  echo "scoped sub-resource), add it to EXEMPT_PROJECT_PIN_GCP in"
  echo "tests/lint-gcp-project-pin.sh with a one-line rationale identifying the"
  echo "parent attribute. Verify against:"
  echo "  registry.terraform.io/providers/hashicorp/google/latest/docs/resources/<name>"
  exit 1
fi

echo "PASS: All GCP resources and modules pin project = var.project_id (or are explicitly exempt)."

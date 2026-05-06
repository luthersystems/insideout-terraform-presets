#!/usr/bin/env bash
#
# Stage 2d (#264) GCP discover smoke. Manual / opt-in — NOT a CI gate.
#
# Cloud Asset Inventory has no first-party emulator (issue #264 confirms;
# the LocalStack equivalent does not exist for GCP). This script runs the
# AWS LocalStack-gate's contract against a real GCP sandbox project the
# operator supplies via $GCP_PROJECT_ID:
#   1. Apply the seed stack at tests/testdata/gcp-seed/ (creates one of
#      each of 5 supported GCP resource types).
#   2. Run `insideout-import discover --provider gcp …` against the real
#      Cloud Asset Inventory API.
#   3. Defense-in-depth: independently run
#      `terraform plan -detailed-exitcode` against the discover-generated
#      genconfig workdir and assert exit 0.
#   4. Sanity-check the manifest contains the 5 seeded resource types.
#
# Local invocation:
#   gcloud auth application-default login   # ADC the discoverer needs
#   GCP_PROJECT_ID=my-sandbox-12345 \
#     bash tests/gcp-discover-smoke.sh
#
# Requirements on the operator's environment:
#   - gcloud ADC configured (the script does NOT prompt for login).
#   - The sandbox project has the Cloud Asset API enabled
#     (`gcloud services enable cloudasset.googleapis.com`).
#   - The operator's principal can write each of the 5 resource types AND
#     read Cloud Asset (roles/cloudasset.viewer + per-service editor).
#   - Terraform 1.5+ on PATH.
#
# Exit codes:
#   0  smoke passed (zero-drift round trip + manifest coverage).
#   1  any step failed.
#   2  prerequisite missing (skipped — not a hard fail in CI).

set -euo pipefail

readonly REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
readonly SEED_DIR="${REPO_ROOT}/tests/testdata/gcp-seed"
readonly OUT_DIR="${REPO_ROOT}/.tmp/discover-gcp-264"

readonly STACK_PROJECT="${STACK_PROJECT:-io-smoke-264}"
readonly REGION="${GCP_REGION:-us-central1}"

log() { printf '\n>>> %s\n' "$*" >&2; }
fail() { printf '\nFAIL: %s\n' "$*" >&2; exit 1; }
skip() { printf '\nSKIP: %s\n' "$*" >&2; exit 2; }

# ---------------------------------------------------------------------------
# Preflight: GCP_PROJECT_ID + ADC.
# ---------------------------------------------------------------------------

if [[ -z "${GCP_PROJECT_ID:-}" ]]; then
  skip "set GCP_PROJECT_ID to a sandbox GCP project to run this smoke"
fi

if ! command -v gcloud >/dev/null 2>&1; then
  skip "gcloud not on PATH — install the Cloud SDK or skip this smoke"
fi

if ! gcloud auth application-default print-access-token >/dev/null 2>&1; then
  skip "no Application Default Credentials — run 'gcloud auth application-default login'"
fi

# ---------------------------------------------------------------------------
# 1. Apply the seed stack.
# ---------------------------------------------------------------------------

log "Applying seed stack at ${SEED_DIR} against project ${GCP_PROJECT_ID}"
export TF_VAR_project_id="${GCP_PROJECT_ID}"
export TF_VAR_stack_project="${STACK_PROJECT}"
export TF_VAR_region="${REGION}"
( cd "${SEED_DIR}" && terraform init -input=false -no-color )
( cd "${SEED_DIR}" && terraform apply -auto-approve -input=false -no-color )

# ---------------------------------------------------------------------------
# 2. Run insideout-import discover against real GCP.
# ---------------------------------------------------------------------------

mkdir -p "${OUT_DIR}"
rm -rf "${OUT_DIR:?}"/*

log "Running insideout-import discover --provider gcp --gcp-project-id ${GCP_PROJECT_ID}"
( cd "${REPO_ROOT}" && go run ./cmd/insideout-import discover \
    --provider gcp \
    --project "${STACK_PROJECT}" \
    --gcp-project-id "${GCP_PROJECT_ID}" \
    --region "${REGION}" \
    --output-dir "${OUT_DIR}" )

# ---------------------------------------------------------------------------
# 3. Apply the import blocks, then assert plan is empty.
# ---------------------------------------------------------------------------

GENCONFIG_DIR="${OUT_DIR}/genconfig"
if [[ ! -d "${GENCONFIG_DIR}" ]]; then
  fail "expected ${GENCONFIG_DIR} to exist after discover"
fi

log "Applying import blocks against ${GCP_PROJECT_ID} to hydrate state"
( cd "${GENCONFIG_DIR}" && terraform apply -auto-approve -input=false -no-color )

log "Re-asserting plan-clean with terraform plan -detailed-exitcode"
set +e
( cd "${GENCONFIG_DIR}" && terraform plan -detailed-exitcode -input=false -no-color )
plan_rc=$?
set -e
case "${plan_rc}" in
  0) log "plan: no changes — zero-drift contract holds" ;;
  1) fail "plan errored (rc=1)" ;;
  2) fail "plan shows changes (rc=2) — post-import drift" ;;
  *) fail "plan unexpected rc=${plan_rc}" ;;
esac

# ---------------------------------------------------------------------------
# 4. Manifest sanity check. Each of the 5 seeded types must appear.
# ---------------------------------------------------------------------------

MANIFEST="${OUT_DIR}/imported.json"
[[ -s "${MANIFEST}" ]] || fail "manifest ${MANIFEST} missing or empty"

required_types=(
  google_compute_network
  google_pubsub_subscription
  google_pubsub_topic
  google_secret_manager_secret
  google_storage_bucket
)
for t in "${required_types[@]}"; do
  if ! jq -e --arg t "${t}" 'any(.[]; .identity.type == $t)' "${MANIFEST}" >/dev/null; then
    fail "manifest missing resource of type ${t}"
  fi
done
log "manifest contains all 5 required resource types"

log "OK — Stage 2d (#264) GCP discover smoke passed"

#!/usr/bin/env bash
#
# Stage 2c4 LocalStack discover gate (#272).
#
# End-to-end zero-drift assertion for `insideout-import discover`:
#   1. Wait for LocalStack to report healthy on http://localhost:4566.
#   2. terraform apply the seed stack at tests/testdata/localstack-seed/
#      (creates one of each of 8 supported AWS resource types — SQS is
#      excluded; see seed's main.tf header for the LocalStack URL-shape
#      reason).
#   3. Run `insideout-import discover --aws-endpoint-url …` against
#      LocalStack. The orchestrator's exit-code contract is: 0 iff the
#      generated stack would `terraform plan` clean.
#   4. Defense-in-depth: independently run
#      `terraform plan -detailed-exitcode` against the discover-generated
#      genconfig workdir and assert exit 0.
#   5. Sanity-check the manifest contains at least the seeded resources.
#
# Local invocation:
#   docker run --rm -d -p 4566:4566 \
#     -v /var/run/docker.sock:/var/run/docker.sock \
#     -e SERVICES=lambda,dynamodb,logs,secretsmanager,sqs,iam,kms,s3,sts \
#     -e DEFAULT_REGION=us-east-1 \
#     --name localstack-272 localstack/localstack:4
#   bash tests/localstack-discover-gate.sh
#   docker rm -f localstack-272
#
# Notes:
#   - LocalStack 4 (not 3) is required: Lambda + DynamoDB seed apply
#     fail on 3.x with state-poll / Docker-availability gaps.
#   - docker.sock mount is required so LocalStack 4 can spawn the
#     Lambda runtime container for deployment-package validation.
#
# CI invocation: as a step inside the localstack-discover-gate job, with
# LocalStack as a `services:` container on the same network.

set -euo pipefail

readonly LOCALSTACK_URL="${LOCALSTACK_URL:-http://localhost:4566}"
readonly REGION="${AWS_REGION:-us-east-1}"
readonly PROJECT="localstack-seed-272"
readonly REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
readonly SEED_DIR="${REPO_ROOT}/tests/testdata/localstack-seed"
readonly OUT_DIR="${REPO_ROOT}/.tmp/discover-272"

# CI exports these via the job env. Local invocations may not, so set
# placeholders; LocalStack ignores credential values when
# skip_credentials_validation is true on the provider.
export AWS_ACCESS_KEY_ID="${AWS_ACCESS_KEY_ID:-test}"
export AWS_SECRET_ACCESS_KEY="${AWS_SECRET_ACCESS_KEY:-test}"
export AWS_REGION="${REGION}"
export AWS_DEFAULT_REGION="${REGION}"

log() { printf '\n>>> %s\n' "$*" >&2; }
fail() { printf '\nFAIL: %s\n' "$*" >&2; exit 1; }

# ---------------------------------------------------------------------------
# 1. LocalStack health.
# ---------------------------------------------------------------------------

log "Waiting for LocalStack at ${LOCALSTACK_URL}/_localstack/health"
for i in $(seq 1 60); do
  if curl --silent --fail "${LOCALSTACK_URL}/_localstack/health" >/dev/null 2>&1; then
    log "LocalStack ready (took ${i}s)"
    break
  fi
  if [[ "${i}" == "60" ]]; then
    fail "LocalStack did not become healthy within 60s"
  fi
  sleep 1
done

# ---------------------------------------------------------------------------
# 2. Apply the seed stack.
# ---------------------------------------------------------------------------

log "Applying seed stack at ${SEED_DIR}"
( cd "${SEED_DIR}" && terraform init -input=false -no-color )
( cd "${SEED_DIR}" && terraform apply -auto-approve -input=false -no-color )

# ---------------------------------------------------------------------------
# 3. Run insideout-import discover against LocalStack.
# ---------------------------------------------------------------------------

mkdir -p "${OUT_DIR}"
rm -rf "${OUT_DIR:?}"/*

log "Running insideout-import discover against ${LOCALSTACK_URL}"
( cd "${REPO_ROOT}" && go run ./cmd/insideout-import discover \
    --provider aws \
    --project "${PROJECT}" \
    --region "${REGION}" \
    --output-dir "${OUT_DIR}" \
    --aws-endpoint-url "${LOCALSTACK_URL}" )

# ---------------------------------------------------------------------------
# 4. Apply the import blocks, then assert plan is empty.
#
# After discover the genconfig workdir contains import {} blocks plus
# generated.tf, but state is empty. A naked `terraform plan -detailed-
# exitcode` legitimately reports `N to import` (rc=2) — that's the
# imports waiting to happen, not drift. The zero-drift contract is:
# once the imports are applied (state hydrated from cloud), the next
# plan must be empty.
#
# `terraform apply -auto-approve` executes the imports against
# LocalStack. If the orchestrator's drift-fix loop didn't fully
# converge, the apply itself will fail (an attribute mismatch surfaces
# as a plan diff during apply's pre-check). Then we re-plan with
# -detailed-exitcode and require 0.
# ---------------------------------------------------------------------------

GENCONFIG_DIR="${OUT_DIR}/genconfig"
if [[ ! -d "${GENCONFIG_DIR}" ]]; then
  fail "expected ${GENCONFIG_DIR} to exist after discover"
fi

log "Applying import blocks against LocalStack to hydrate state"
( cd "${GENCONFIG_DIR}" && terraform apply -auto-approve -input=false -no-color )

log "Re-asserting plan-clean with terraform plan -detailed-exitcode"
# -detailed-exitcode codes: 0 = no diff, 1 = error, 2 = diff.
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
# 5. Manifest sanity check. Each of the 9 seeded types must appear.
# ---------------------------------------------------------------------------

MANIFEST="${OUT_DIR}/imported.json"
[[ -s "${MANIFEST}" ]] || fail "manifest ${MANIFEST} missing or empty"

required_types=(
  aws_cloudwatch_log_group
  aws_dynamodb_table
  aws_iam_policy
  aws_iam_role
  aws_kms_key
  aws_lambda_function
  aws_s3_bucket
  aws_secretsmanager_secret
)
for t in "${required_types[@]}"; do
  if ! jq -e --arg t "${t}" 'any(.[]; .identity.type == $t)' "${MANIFEST}" >/dev/null; then
    fail "manifest missing resource of type ${t}"
  fi
done
log "manifest contains all 8 required resource types"

log "OK — Stage 2c4 zero-drift gate passed"

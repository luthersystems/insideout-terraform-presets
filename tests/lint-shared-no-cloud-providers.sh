#!/usr/bin/env bash
# Static analysis: top-level _shared/<name>/ helpers (cross-cloud helpers, see
# issue #203) MUST NOT declare any cloud-specific provider. Cross-cloud
# helpers ride along with both AWS-only and GCP-only stacks, so dragging a
# cloud-specific provider into them would force every consumer to install
# that provider.
#
# Per-cloud helpers under aws/_shared/ and gcp/_shared/ are NOT scanned by
# this script — they may freely declare their cloud's provider.
#
# Forbidden providers: aws, google, google-beta, azurerm, azuread,
# kubernetes (cluster-specific), helm.
# Permitted providers: null, random, http, time, local, external, tls,
# archive, hashicorp/* generic providers.
#
# Detection: looks for `<provider> = { ... }` entries inside `required_providers`
# blocks AND for top-level `provider "<provider>" {}` blocks.
#
# Cardinality floor: the script asserts at least one cross-cloud module was
# scanned per run — silent zero-module passes (e.g. someone deletes the
# bucket) defeat the test's purpose.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SHARED_DIR="$REPO_ROOT/_shared"

# Cloud-specific providers that cross-cloud helpers must NOT declare.
# Keep alphabetised.
FORBIDDEN_PROVIDERS=(
  aws
  azuread
  azurerm
  google
  google-beta
  helm
  kubernetes
)

forbidden_alt="$(IFS='|'; echo "${FORBIDDEN_PROVIDERS[*]}")"

echo "=== Checking _shared/ (cross-cloud) helpers for cloud-specific providers ==="
echo

if [ ! -d "$SHARED_DIR" ]; then
  echo "FAIL: $SHARED_DIR does not exist; cross-cloud bucket missing."
  echo "      Issue #203 reserves _shared/ for cloud-agnostic helpers."
  exit 1
fi

scanned=0
any_fail=0

# Iterate every top-level _shared/<name>/ directory (skip files at the bucket root).
for moddir in "$SHARED_DIR"/*/; do
  [ -d "$moddir" ] || continue
  scanned=$((scanned + 1))

  # Scan every .tf file in this module for forbidden provider declarations.
  for tf in "$moddir"*.tf; do
    [ -f "$tf" ] || continue

    # Pattern 1: forbidden provider listed in a required_providers block.
    #   required_providers {
    #     aws = { source = "hashicorp/aws" ... }
    #   }
    # We match a leading-whitespace bare provider name followed by '=' followed by '{'.
    bad=$(awk -v re="^[[:space:]]+(${forbidden_alt})[[:space:]]*=[[:space:]]*\\{" '
      $0 ~ re {
        printf "ERROR: %s:%d: cross-cloud helper declares forbidden provider in required_providers: %s\n", FILENAME, NR, $0
      }
    ' "$tf")
    if [ -n "$bad" ]; then
      echo "$bad"
      any_fail=1
    fi

    # Pattern 2: top-level provider "aws" {}-style block.
    bad=$(awk -v re="^[[:space:]]*provider[[:space:]]+\"(${forbidden_alt})\"" '
      $0 ~ re {
        printf "ERROR: %s:%d: cross-cloud helper declares forbidden top-level provider block: %s\n", FILENAME, NR, $0
      }
    ' "$tf")
    if [ -n "$bad" ]; then
      echo "$bad"
      any_fail=1
    fi

    # Pattern 3: module source references that pull a cloud-specific upstream
    # registry module (e.g. terraform-aws-modules/*, terraform-google-modules/*).
    bad=$(awk '
      /source[[:space:]]*=[[:space:]]*"terraform-(aws|google|azurerm)-modules\// {
        printf "ERROR: %s:%d: cross-cloud helper sources a cloud-specific registry module: %s\n", FILENAME, NR, $0
      }
    ' "$tf")
    if [ -n "$bad" ]; then
      echo "$bad"
      any_fail=1
    fi
  done
done

if (( scanned == 0 )); then
  echo "FAIL: scanned zero cross-cloud helper modules under $SHARED_DIR/."
  echo "      Issue #203 requires at least one (the _smoke fixture qualifies)."
  exit 1
fi

if (( any_fail )); then
  echo
  echo "FAIL: One or more cross-cloud helpers declare a cloud-specific provider."
  echo "      Cross-cloud helpers ride along with both AWS-only and GCP-only"
  echo "      stacks; they MUST NOT pull in aws / google / google-beta /"
  echo "      azurerm. If the helper genuinely needs a cloud API, move it to"
  echo "      aws/_shared/<name>/ or gcp/_shared/<name>/ instead."
  exit 1
fi

echo "PASS: All $scanned cross-cloud helper module(s) under _shared/ are provider-clean."

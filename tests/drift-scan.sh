#!/usr/bin/env bash
# CI gate: cross-check every generated typed model
# (pkg/composer/imported/generated) against the REAL provider schema at the
# exact version the composer pins, hunting the odb_network_arn bug class
# (#839): a nested-object attribute the emitter renders as an object literal
# (`attr = [{...}]`) that is missing a provider-Required sub-attribute. Such a
# model fails `terraform plan` the moment the provider adds the sub-attr.
#
# The provider version is NOT hardcoded here. We compose a minimal aws_s3
# stack with cmd/composetest; the composed root's providers.tf pins the exact
# version from the composer's single source of truth
# (pkg/composer/imported/provider_pins.go — `= 6.52.0` today). `terraform
# init` then resolves that pin and `terraform providers schema -json` dumps
# the matching schema, which cmd/driftscan diffs against the models.
#
# Known-stale, issue-tracked misses live in tests/driftscan-allowlist.txt and
# print as ALLOWED(stale-model) rather than failing the gate (#845). The
# allowlist can only shrink — driftscan hard-errors on any entry without an
# issue citation. See #844.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

dir="$(mktemp -d)"
trap 'rm -rf "$dir"' EXIT

echo "=== driftscan: composing aws_s3 to read the pinned provider version ==="
go run ./cmd/composetest -keys aws_s3 -out "$dir" -project driftscan -region us-west-2

echo "=== driftscan: terraform init + provider schema dump ==="
terraform -chdir="$dir" init -backend=false -input=false -no-color >/dev/null
terraform -chdir="$dir" providers schema -json >"$dir/schema.json"

echo "=== driftscan: scanning generated models against the live schema ==="
go run ./cmd/driftscan \
  -schema "$dir/schema.json" \
  -allow "$REPO_ROOT/tests/driftscan-allowlist.txt" \
  -fail-on-bugs \
  -quiet

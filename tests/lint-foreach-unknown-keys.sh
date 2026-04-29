#!/usr/bin/env bash
# Static analysis: detect apply-time-unknown values flowing into for_each map
# keys.
#
# Terraform requires for_each map keys to be plan-time-known. Values derived
# from random_id, random_uuid, timestamp(), uuid(), or any computed resource
# attribute (e.g. the .hex of a random_id) are only known after apply, so
# using them as a key fails apply with "Invalid for_each argument" — invisible
# to `terraform validate` and `terraform plan` against a non-GKE state.
#
# This is a tripwire (catches the explicit cases). The load-bearing check is
# pkg/composer/compose_gcp_vpc_plan_integration_test.go, which actually runs
# `terraform plan` and catches every variant of this bug regardless of which
# symbol leaks the unknown value.
#
# Patterns flagged:
#   1. (local.X) = ...  used as a map key, where local.X's RHS contains an
#      apply-time-unknown ref (random_id., random_uuid., timestamp(, uuid().
#   2. for_each = local.X where X is similarly tainted.
#   3. for_each = { ... } where the map-literal line itself directly references
#      an apply-time-unknown value.
#
# The list-of-objects upstream-rekey case (a name field that becomes a
# downstream module's for_each key) is not statically detectable here — the
# integration test covers it dynamically.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
ERRORS_FILE=$(mktemp)
trap 'rm -f "$ERRORS_FILE"' EXIT

# Pattern matching apply-time-unknown references. Uses character classes for
# parens so it parses identically under BSD awk and GNU awk (BSD awk treats a
# bare "(" as the start of a regex group and rejects an unbalanced one).
UNKNOWN_REGEX='random_id\.|random_uuid\.|timestamp[(]|uuid[(]'

echo "=== Checking for apply-time-unknown values in for_each map keys ==="
echo

for mainfile in "$REPO_ROOT"/{aws,gcp}/*/main.tf; do
  [ -f "$mainfile" ] || continue

  # Extract names of locals whose single-line RHS contains a tainted ref.
  # Presets use one-local-per-line exclusively (verified across the repo); a
  # multi-line scan is unnecessary.
  tainted=$(awk -v re="$UNKNOWN_REGEX" '
    /^locals[ \t]*\{/ { in_locals=1; next }
    in_locals && /^\}/ { in_locals=0; next }
    in_locals && /^[ \t]*[A-Za-z_][A-Za-z0-9_]*[ \t]*=/ {
      if ($0 ~ re) {
        line = $0
        sub(/^[ \t]+/, "", line)
        sub(/[ \t]*=.*$/, "", line)
        print line
      }
    }
  ' "$mainfile")

  # Pattern 1 + 2 per tainted local.
  while read -r name; do
    [ -z "$name" ] && continue

    matches=$(grep -nE "\(local\.${name}\)[ \t]*=" "$mainfile" || true)
    if [ -n "$matches" ]; then
      while IFS=: read -r lineno content; do
        echo "ERROR: $mainfile:$lineno: tainted local.$name used as map key (RHS contains apply-time-unknown ref)" | tee -a "$ERRORS_FILE"
        echo "  $(echo "$content" | sed 's/^[[:space:]]*//')"
      done <<< "$matches"
    fi

    matches=$(grep -nE "for_each[ \t]*=[ \t]*local\.${name}\b" "$mainfile" || true)
    if [ -n "$matches" ]; then
      while IFS=: read -r lineno content; do
        echo "ERROR: $mainfile:$lineno: for_each consumes tainted local.$name (RHS contains apply-time-unknown ref)" | tee -a "$ERRORS_FILE"
        echo "  $(echo "$content" | sed 's/^[[:space:]]*//')"
      done <<< "$matches"
    fi
  done <<< "$tainted"

  # Pattern 3: for_each line itself directly references an apply-time-unknown value.
  matches=$(grep -nE "for_each[ \t]*=" "$mainfile" | grep -E "$UNKNOWN_REGEX" || true)
  if [ -n "$matches" ]; then
    while IFS=: read -r lineno content; do
      echo "ERROR: $mainfile:$lineno: for_each line directly references apply-time-unknown value" | tee -a "$ERRORS_FILE"
      echo "  $(echo "$content" | sed 's/^[[:space:]]*//')"
    done <<< "$matches"
  fi
done

if [ -s "$ERRORS_FILE" ]; then
  echo
  echo "FAIL: Apply-time-unknown values found in for_each map keys."
  echo "      These break apply with 'Invalid for_each argument' and are"
  echo "      invisible to 'terraform validate'. See issue #163 for context."
  exit 1
fi

echo "PASS: No apply-time-unknown values found in for_each map keys."

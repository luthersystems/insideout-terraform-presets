#!/usr/bin/env bash
# Static analysis: detect sensitive variables used in for_each without nonsensitive().
#
# Terraform rejects sensitive values (or values derived from them) as for_each
# arguments at plan time. This is invisible to `terraform validate` and only
# surfaces during `terraform apply` — often in production.
#
# This script parses all modules and flags any for_each that references a
# sensitive variable without wrapping it in nonsensitive().

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
ERRORS_FILE=$(mktemp)
trap 'rm -f "$ERRORS_FILE"' EXIT

echo "=== Checking for sensitive variables in for_each expressions ==="
echo

for varfile in "$REPO_ROOT"/{aws,gcp}/*/variables.tf; do
  [ -f "$varfile" ] || continue
  dir=$(dirname "$varfile")
  mainfile="$dir/main.tf"
  [ -f "$mainfile" ] || continue

  # Extract variable names that have sensitive = true.
  sensitive_vars=$(awk '
    /^variable[ \t]+"/ { name=$2; gsub(/"/, "", name) }
    /sensitive[ \t]*=[ \t]*true/ { if (name != "") print name; name="" }
  ' "$varfile")

  [ -z "$sensitive_vars" ] && continue

  while read -r varname; do
    [ -z "$varname" ] && continue
    pattern="var[.]${varname}"

    # Check for_each lines referencing this variable (|| true to avoid pipefail exit)
    matches=$(grep -n "for_each" "$mainfile" | grep "$pattern" || true)
    if [ -n "$matches" ]; then
      while IFS=: read -r lineno content; do
        if ! echo "$content" | grep -q "nonsensitive"; then
          echo "ERROR: $mainfile:$lineno: sensitive variable '$varname' in for_each without nonsensitive()" | tee -a "$ERRORS_FILE"
          echo "  $(echo "$content" | sed 's/^[[:space:]]*//')"
        fi
      done <<< "$matches"
    fi

    # Check locals (for ... in var.X) that may feed for_each
    matches=$(grep -n "for.*in.*${pattern}" "$mainfile" | grep -v "for_each" || true)
    if [ -n "$matches" ]; then
      while IFS=: read -r lineno content; do
        if ! echo "$content" | grep -q "nonsensitive"; then
          echo "ERROR: $mainfile:$lineno: sensitive variable '$varname' in expression without nonsensitive()" | tee -a "$ERRORS_FILE"
          echo "  $(echo "$content" | sed 's/^[[:space:]]*//')"
        fi
      done <<< "$matches"
    fi
  done <<< "$sensitive_vars"
done

if [ -s "$ERRORS_FILE" ]; then
  echo
  echo "FAIL: Found sensitive variables used without nonsensitive() wrapper"
  exit 1
fi

echo "PASS: No sensitive variables found in for_each without nonsensitive()"

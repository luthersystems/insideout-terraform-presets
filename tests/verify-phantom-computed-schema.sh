#!/usr/bin/env bash
# Validates phantom-computed-fields.txt against a `terraform providers
# schema -json` dump: every entry must reference a real attribute on a real
# resource type AND that attribute must be pure-Computed in the provider
# schema (computed=true, optional!=true, required!=true).
#
# This catches the failure mode the user asked about: when a provider
# upgrade flips a field from pure-Computed to Optional+Computed (or vice
# versa, or removes it entirely), the denylist silently goes stale and
# downstream drift-check filtering either misses real drift or filters the
# wrong fields. Running this in CI on every PR (against the
# schemas/providers.tf-pinned providers) closes that loop.
#
# Usage
#   tests/verify-phantom-computed-schema.sh <schema.json>
#
# In CI: a wrapper job runs `terraform init` + `terraform providers schema
# -json` against schemas/providers.tf, pipes the output into a tmp file,
# then invokes this script.

set -euo pipefail

if [ "${1:-}" = "" ]; then
  echo "usage: $0 <schema.json>" >&2
  exit 2
fi

SCHEMA="$1"
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DENYLIST="$REPO_ROOT/phantom-computed-fields.txt"

if [ ! -f "$SCHEMA" ]; then
  echo "ERROR: schema file not found: $SCHEMA" >&2
  exit 1
fi
if [ ! -f "$DENYLIST" ]; then
  echo "ERROR: denylist not found: $DENYLIST" >&2
  exit 1
fi
command -v jq >/dev/null || { echo "ERROR: jq not on PATH" >&2; exit 1; }

echo "=== Validating phantom-computed-fields.txt against provider schema ==="
echo

any_fail=0
total=0
ok=0

while IFS= read -r entry; do
  [ -n "$entry" ] || continue
  total=$((total + 1))

  res_type="${entry%%.*}"
  attr="${entry#*.}"

  case "$res_type" in
    aws_*)
      provider_key='registry.terraform.io/hashicorp/aws'
      ;;
    google_*)
      provider_key='registry.terraform.io/hashicorp/google'
      ;;
    *)
      echo "ERROR: $entry — unknown resource-type prefix (expected aws_* or google_*)."
      any_fail=1
      continue
      ;;
  esac

  # Look up the resource and attribute. jq exits non-zero only on parse
  # errors; missing keys yield `null`, which we test for explicitly.
  attr_json=$(jq -c --arg p "$provider_key" --arg t "$res_type" --arg a "$attr" \
    '.provider_schemas[$p].resource_schemas[$t].block.attributes[$a] // null' \
    "$SCHEMA")

  if [ "$attr_json" = "null" ]; then
    # Could be a missing resource type or a missing attribute. Differentiate
    # so the operator gets a useful message.
    res_present=$(jq -c --arg p "$provider_key" --arg t "$res_type" \
      '.provider_schemas[$p].resource_schemas[$t] // null' "$SCHEMA")
    if [ "$res_present" = "null" ]; then
      echo "ERROR: $entry — resource type '$res_type' not in provider schema (provider upgrade removed it, or denylist has a typo)."
    else
      echo "ERROR: $entry — attribute '$attr' not on resource '$res_type' (provider schema knows the resource but not this attr)."
    fi
    any_fail=1
    continue
  fi

  computed=$(echo "$attr_json" | jq -r '.computed // false')
  optional=$(echo "$attr_json" | jq -r '.optional // false')
  required=$(echo "$attr_json" | jq -r '.required // false')

  if [ "$computed" != "true" ]; then
    echo "ERROR: $entry — attribute is not Computed in the provider schema (computed=$computed). Phantom-drift filtering does not apply; remove from denylist."
    any_fail=1
    continue
  fi

  if [ "$optional" = "true" ] || [ "$required" = "true" ]; then
    echo "ERROR: $entry — attribute is Optional+Computed (optional=$optional required=$required), not pure-Computed. lifecycle.ignore_changes WILL work on it; fix upstream in the module and remove from denylist."
    any_fail=1
    continue
  fi

  ok=$((ok + 1))
done < <(grep -v '^#' "$DENYLIST" | grep -v '^[[:space:]]*$')

echo
echo "Summary: $ok / $total entries pass schema validation."

if [ "$any_fail" -ne 0 ]; then
  echo
  echo "FAIL: phantom-computed-fields.txt drifted from provider schema."
  echo "Re-run after $REPO_ROOT/schemas/providers.tf is updated, or fix the denylist entries flagged above."
  exit 1
fi

echo "PASS: every denylist entry is pure-Computed in the pinned provider schema."

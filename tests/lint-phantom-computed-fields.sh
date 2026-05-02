#!/usr/bin/env bash
# Static analysis: every entry in phantom-computed-fields.txt must have a
# matching `# NOTE: ... drift-check level — see sandbox-infrastructure-
# template#93` comment in its module main.tf, and vice versa. Keeps the
# in-repo annotations and the externally-consumed denylist in lockstep.
#
# phantom-computed-fields.txt is the cross-repo data file consumed by
# sandbox-infrastructure-template's drift-check wrapper to filter out pure-
# Computed phantom drift (terraform#30517 — lifecycle.ignore_changes is a
# no-op on Computed-only fields, so suppression has to live at the
# orchestration layer). See issue #215 for the design discussion.
#
# When this check fails:
#   * Missing NOTE comment in module: open the relevant aws/<m>/main.tf or
#     gcp/<m>/main.tf, find the resource named `<resource_type>`, and add
#     the canonical block (see aws/cognito/main.tf:76-78 for wording).
#   * Stale entry in the txt file: confirm the field is still pure Computed
#     in the provider schema (`terraform providers schema -json`). If it
#     became Optional+Computed, fix it upstream via lifecycle.ignore_changes
#     and remove the entry.
#   * NOTE in module without matching txt entry: add the missing line to
#     phantom-computed-fields.txt (alphabetically sorted within its
#     AWS/GCP section).

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DENYLIST="$REPO_ROOT/phantom-computed-fields.txt"

if [ ! -f "$DENYLIST" ]; then
  echo "ERROR: $DENYLIST not found." >&2
  exit 1
fi

echo "=== Cross-checking phantom-computed-fields.txt ↔ module NOTE comments ==="
echo

any_fail=0

# Load entries (bash 3.2 compatible — no mapfile / readarray).
ENTRIES=()
while IFS= read -r line; do
  [ -n "$line" ] && ENTRIES+=("$line")
done < <(grep -v '^#' "$DENYLIST" | grep -v '^[[:space:]]*$')

# Direction 1: every txt entry must have a matching NOTE comment in some
# aws/<m>/*.tf or gcp/<m>/*.tf. The NOTE must mention BOTH the attribute
# name and the canonical "drift-check level — see sandbox-infrastructure-
# template#93" phrase.
for entry in "${ENTRIES[@]}"; do
  res_type="${entry%%.*}"
  attr="${entry#*.}"

  case "$res_type" in
    aws_*)    search_dir="$REPO_ROOT/aws" ;;
    google_*) search_dir="$REPO_ROOT/gcp" ;;
    *)
      echo "ERROR: $entry has unknown resource-type prefix (expected aws_* or google_*)."
      any_fail=1
      continue
      ;;
  esac

  # Look for any .tf file under the cloud bucket that contains BOTH the
  # resource type AND a NOTE block naming the attribute and the
  # sandbox-infrastructure-template#93 phrase.
  found=0
  while IFS= read -r f; do
    grep -q "resource \"$res_type\"" "$f" || continue
    if grep -B0 -A4 -E "^[[:space:]]*#[[:space:]]+NOTE:.*\b${attr}\b" "$f" \
        | grep -q "drift-check level — see sandbox-infrastructure-template#93"; then
      found=1
      break
    fi
  done < <(find "$search_dir" -type f -name '*.tf' 2>/dev/null)

  if [ "$found" -eq 0 ]; then
    echo "ERROR: $entry listed in phantom-computed-fields.txt but no NOTE block in $search_dir/<m>/*.tf names attribute '$attr' alongside 'drift-check level — see sandbox-infrastructure-template#93'."
    any_fail=1
  fi
done

# Direction 2: every NOTE block must have a matching txt entry. We extract
# the resource type from the surrounding `resource "..."` declaration and
# the attribute name from the NOTE text.
note_grep_output=$(grep -rn -E "^[[:space:]]*#[[:space:]]+NOTE:.*drift" \
  "$REPO_ROOT/aws" "$REPO_ROOT/gcp" 2>/dev/null \
  | grep "drift-check level — see sandbox-infrastructure-template#93" || true)

while IFS= read -r note_match; do
  [ -n "$note_match" ] || continue
  file="${note_match%%:*}"
  rest="${note_match#*:}"
  lineno="${rest%%:*}"

  # Walk backward from the NOTE line to find the enclosing
  # `resource "<type>" "<name>"` block.
  res_type=$(awk -v target="$lineno" '
    /^resource "/ {
      gsub(/"/, "", $2)
      current=$2
    }
    NR == target { print current; exit }
  ' "$file")

  if [ -z "$res_type" ]; then
    continue
  fi

  # Pull the attribute name from the NOTE text. The canonical shape is
  # "# NOTE: <attr> drifts ..." or "# NOTE: <attr1> and <attr2> drift ...";
  # we extract the first word after "NOTE:". Multi-attribute NOTE blocks
  # are tolerated — direction 1 will surface any unlisted attributes.
  attr=$(sed -n "${lineno}p" "$file" \
    | sed -E 's/^[[:space:]]*#[[:space:]]*NOTE:[[:space:]]*//; s/[[:space:]].*$//')

  if [ -z "$attr" ]; then
    continue
  fi

  entry="$res_type.$attr"
  matched=0
  for e in "${ENTRIES[@]}"; do
    if [ "$e" = "$entry" ]; then
      matched=1
      break
    fi
  done
  if [ "$matched" -eq 0 ]; then
    echo "ERROR: $file:$lineno NOTE block names '$attr' on resource '$res_type' but '$entry' is not in phantom-computed-fields.txt."
    any_fail=1
  fi
done <<< "$note_grep_output"

if [ "$any_fail" -ne 0 ]; then
  echo
  echo "FAIL: phantom-computed-fields.txt is out of sync with module NOTE comments."
  exit 1
fi

echo "PASS: phantom-computed-fields.txt and module NOTE comments are in sync."

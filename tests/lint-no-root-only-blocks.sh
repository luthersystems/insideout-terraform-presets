#!/usr/bin/env bash
# Static analysis: preset modules must NOT contain top-level `import {}`
# or `removed {}` blocks. Both are TF 1.5+ root-module-only constructs —
# when a preset is composed as a child module (the actual consumption
# shape via the InsideOut composer), terraform init fails with:
#
#   Error: Invalid import configuration
#     An import block was detected in "module.<name>". Import blocks
#     are only allowed in the root module.
#
# This regressed the v0.7.0 idempotency fix for gcp/identity_platform
# (issue #199); v0.7.1 reverted it. Adoption-via-import for singleton
# resources must be emitted by the composer at the root, not by the
# preset itself.
#
# Scope: every aws/<module>/*.tf and gcp/<module>/*.tf. Walks each line
# looking for a leading-column `import {` or `removed {` token,
# excluding HCL comments (`#`, `//`) and matches inside strings.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

echo "=== Checking preset modules for root-only blocks (import {}, removed {}) ==="
echo

any_fail=0
while IFS= read -r f; do
  [ -f "$f" ] || continue
  # Match a line whose first non-whitespace token is `import {` or
  # `removed {`. AWK is sufficient — these blocks are always declared
  # at the top of a file or after blank/comment lines, never inline.
  bad=$(awk '
    /^[[:space:]]*#/ { next }
    /^[[:space:]]*\/\// { next }
    /^[[:space:]]*import[[:space:]]*\{/ { print FILENAME ":" NR ": top-level import {} block (root-only in TF 1.5+)"; bad=1 }
    /^[[:space:]]*removed[[:space:]]*\{/ { print FILENAME ":" NR ": top-level removed {} block (root-only in TF 1.5+)"; bad=1 }
    END { exit (bad ? 1 : 0) }
  ' "$f") || {
    echo "ERROR in $f:"
    echo "$bad"
    any_fail=1
  }
done < <(find "$REPO_ROOT/aws" "$REPO_ROOT/gcp" -mindepth 2 -maxdepth 2 -name '*.tf' 2>/dev/null | sort)

if (( any_fail )); then
  echo
  echo "FAIL: One or more preset modules contain root-only blocks."
  echo "Fix: remove the import/removed block from the child module. If"
  echo "adoption is required, the InsideOut composer must emit the block"
  echo "at the root, e.g.:"
  echo "  import {"
  echo "    to = module.<name>.<resource_addr>"
  echo "    id = ..."
  echo "  }"
  echo "See issue #199 for the gcp/identity_platform precedent."
  exit 1
fi

echo "PASS: No preset module contains root-only blocks."

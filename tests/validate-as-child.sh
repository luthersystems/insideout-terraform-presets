#!/usr/bin/env bash
# Wrap each preset module as a child of a synthetic root and run
# `terraform init`. Mirrors the actual consumption shape: the composer
# (`luthersystems/reliable`) instantiates every preset as
# `module "<name>" { source = "..." }` in a generated root. A subset of
# Terraform constructs are root-module-only (`import {}`, `removed {}`,
# certain `provider` block forms) and pass standalone `terraform validate`
# but fail at init when nested.
#
# Usage:
#   tests/validate-as-child.sh                # all presets
#   tests/validate-as-child.sh aws/vpc        # one preset
#
# Notes:
# - Init alone catches the root-only-block class of bug (#199). We
#   intentionally do NOT run validate/plan: they require concrete
#   variable values and would force per-module fixtures. Init parses
#   the configuration tree without evaluating variables.
# - The synthetic root passes no arguments; Terraform's parser tolerates
#   missing required variables at init time. Variable validation runs at
#   plan/apply.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TARGET="${1:-}"

if [ -n "$TARGET" ]; then
  if [ ! -d "$REPO_ROOT/$TARGET" ]; then
    echo "ERROR: $REPO_ROOT/$TARGET is not a directory" >&2
    exit 2
  fi
  presets=("$TARGET")
else
  # Avoid mapfile so this runs on macOS bash 3.2 without GNU bash 4+.
  presets=()
  while IFS= read -r d; do
    [ -n "$d" ] && presets+=("$d")
  done < <(find "$REPO_ROOT/aws" "$REPO_ROOT/gcp" -mindepth 1 -maxdepth 1 -type d 2>/dev/null \
    | sort \
    | while read -r dir; do [ -f "$dir/.validate-skip" ] || echo "${dir#"$REPO_ROOT/"}"; done)
fi

echo "=== Validating ${#presets[@]} preset(s) as child modules ==="
echo

# Single shared workdir to amortize provider downloads. Each preset gets
# its own subdir so .terraform/ caches don't collide.
work="$(mktemp -d -t validate-as-child.XXXXXX)"
trap 'rm -rf "$work"' EXIT

any_fail=0
for p in "${presets[@]}"; do
  rootdir="$work/$p"
  mkdir -p "$rootdir"
  cat > "$rootdir/main.tf" <<EOF
# Auto-generated synthetic root for tests/validate-as-child.sh
# Wraps $p as a child module to verify it parses under the actual
# consumption shape (composer-emitted root + module instantiation).
module "child" {
  source = "$REPO_ROOT/$p"
}
EOF
  if out=$(terraform -chdir="$rootdir" init -backend=false -input=false -no-color 2>&1); then
    printf "  PASS  %s\n" "$p"
  else
    printf "  FAIL  %s\n" "$p"
    echo "$out" | sed 's/^/    /'
    any_fail=1
  fi
done

echo
if (( any_fail )); then
  echo "FAIL: One or more presets failed init when wrapped as child modules."
  echo "This is the failure shape the composer hits at deploy time. Common causes:"
  echo "  - Top-level \`import {}\` or \`removed {}\` block (root-only in TF 1.5+; see #199)."
  echo "  - \`provider\` block with arguments inside a child module (root-only since TF 0.13)."
  echo "  - Bad module source path or missing required_providers entry."
  exit 1
fi
echo "PASS: All presets parse correctly when wrapped as child modules."

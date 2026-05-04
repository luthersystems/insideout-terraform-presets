#!/usr/bin/env bash
# Wrap each preset module as a child of a synthetic root and run
# `terraform init`. Mirrors the actual consumption shape: the InsideOut
# composer instantiates every preset as
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
# - aws/_shared/, gcp/_shared/, _shared/ buckets (issue #203) are NOT
#   wrapped as standalone presets — they are internal helpers the composer
#   skips during preset enumeration. They DO get bundled alongside any
#   wrapped preset that references them via `source = "../_shared/<name>"`
#   so that path resolves under a single Terraform "module package"
#   (Terraform rejects `../` source traversal across package boundaries —
#   `Local module path escapes module package`).

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TARGET="${1:-}"

# is_shared_path returns success (0) iff the given preset path lives under a
# leading-underscore "_shared" or "_<bucket>" directory anywhere in its
# ancestry — i.e. it's an internal helper module rather than a top-level
# preset. Mirrors composer.isInternalDirName in pkg/composer/presets.go.
is_shared_path() {
  case "$1" in
    _*|*/_*) return 0 ;;
    *) return 1 ;;
  esac
}

if [ -n "$TARGET" ]; then
  if [ ! -d "$REPO_ROOT/$TARGET" ]; then
    echo "ERROR: $REPO_ROOT/$TARGET is not a directory" >&2
    exit 2
  fi
  if is_shared_path "$TARGET"; then
    echo "ERROR: $TARGET is an internal _shared bucket, not a top-level preset." >&2
    echo "       Internal helpers are validated transitively when a preset that" >&2
    echo "       depends on them is wrapped. See issue #203." >&2
    exit 2
  fi
  presets=("$TARGET")
else
  # Avoid mapfile so this runs on macOS bash 3.2 without GNU bash 4+.
  # find -mindepth 1 -maxdepth 1 enumerates aws/<x> and gcp/<x>; we then
  # filter out any path whose first component starts with `_` (e.g.
  # aws/_shared) — those are bundled, not wrapped. The leaf-level
  # filename-leading-underscore convention (e.g. `_smoke` if it ever
  # appeared at top level) would also be skipped.
  presets=()
  while IFS= read -r d; do
    [ -n "$d" ] || continue
    is_shared_path "$d" && continue
    presets+=("$d")
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

# bundle_pkg_for_preset stages a preset and any shared modules it references
# into a single synthetic Terraform "module package" rooted at $pkg_base. The
# layout mirrors the live repo so the preset's existing `../_shared/<name>`
# (per-cloud) and `../../_shared/<name>` (cross-cloud) source paths resolve
# without rewriting:
#
#   $pkg_base/<cloud>/<preset_name>/  ← wrapped preset
#   $pkg_base/<cloud>/_shared/<name>/ ← per-cloud helpers it references
#   $pkg_base/_shared/<name>/         ← cross-cloud helpers it references
#
# From $pkg_base/<cloud>/<preset_name>/main.tf, `../_shared/<name>` resolves
# to $pkg_base/<cloud>/_shared/<name>/, and `../../_shared/<name>` resolves
# to $pkg_base/_shared/<name>/ — Terraform treats $pkg_base as a single
# module package so the `../` traversal stays inside one package, avoiding
# the "Local module path escapes module package" error that surfaced in #203.
#
# Args:
#   $1 — preset path (e.g. "aws/vpc")
#   $2 — synthetic package base (e.g. "$work/aws/vpc/pkg")
bundle_pkg_for_preset() {
  local preset_path="$1"
  local pkg_base="$2"
  local cloud src
  cloud="${preset_path%%/*}"

  mkdir -p "$pkg_base/$cloud"
  cp -R "$REPO_ROOT/$preset_path" "$pkg_base/$cloud/"

  # Per-cloud shared refs: `../_shared/<name>` from within aws/<m>/ resolves
  # to aws/_shared/<name>. Mirror that layout under $pkg_base/<cloud>/.
  while IFS= read -r ref; do
    [ -n "$ref" ] || continue
    src="$REPO_ROOT/$cloud/_shared/$ref"
    if [ -d "$src" ]; then
      mkdir -p "$pkg_base/$cloud/_shared"
      cp -R "$src" "$pkg_base/$cloud/_shared/"
    fi
  done < <(grep -hE 'source[[:space:]]*=[[:space:]]*"\.\./_shared/[^"]+"' \
             "$REPO_ROOT/$preset_path"/*.tf 2>/dev/null \
             | sed -E 's|.*"\.\./_shared/([^"]+)".*|\1|' \
             | sort -u)

  # Cross-cloud shared refs: `../../_shared/<name>` from within aws/<m>/
  # resolves to ./_shared/<name> at the repo root. Mirror under $pkg_base/.
  while IFS= read -r ref; do
    [ -n "$ref" ] || continue
    src="$REPO_ROOT/_shared/$ref"
    if [ -d "$src" ]; then
      mkdir -p "$pkg_base/_shared"
      cp -R "$src" "$pkg_base/_shared/"
    fi
  done < <(grep -hE 'source[[:space:]]*=[[:space:]]*"\.\./\.\./_shared/[^"]+"' \
             "$REPO_ROOT/$preset_path"/*.tf 2>/dev/null \
             | sed -E 's|.*"\.\./\.\./_shared/([^"]+)".*|\1|' \
             | sort -u)
}

any_fail=0
for p in "${presets[@]}"; do
  rootdir="$work/$p"
  mkdir -p "$rootdir"

  # Detect whether the preset references any shared modules. If so, build a
  # synthetic "package" workdir under $rootdir/pkg/ that co-locates the
  # preset with its shared deps so `../_shared/<name>` resolves within a
  # single Terraform module package (Terraform rejects `../` source
  # traversal across package boundaries).
  needs_pkg=0
  if grep -qE 'source[[:space:]]*=[[:space:]]*"\.\./(\.\./)?_shared/' "$REPO_ROOT/$p"/*.tf 2>/dev/null; then
    needs_pkg=1
  fi

  if (( needs_pkg )); then
    pkg_base="$rootdir/pkg"
    bundle_pkg_for_preset "$p" "$pkg_base"
    cat > "$rootdir/main.tf" <<EOF
# Auto-generated synthetic root for tests/validate-as-child.sh
# Wraps $p (with bundled _shared/ deps) as a child module to verify it
# parses under the actual consumption shape (composer-emitted root +
# module instantiation + bundled shared helpers; see issue #203).
module "child" {
  source = "./pkg/$p"
}
EOF
  else
    cat > "$rootdir/main.tf" <<EOF
# Auto-generated synthetic root for tests/validate-as-child.sh
# Wraps $p as a child module to verify it parses under the actual
# consumption shape (composer-emitted root + module instantiation).
module "child" {
  source = "$REPO_ROOT/$p"
}
EOF
  fi

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

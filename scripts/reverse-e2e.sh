#!/usr/bin/env bash
#
# reverse-e2e.sh — end-to-end reverse-import harness against a REAL AWS account.
#
# Drives the PRODUCTION engine the exact way Mars does — insideout-import
# `reverse` → pkg/reverseimport.Run — through every stage:
#
#   discover (identities) → genconfig → driftfix → depchase → final terraform plan
#
# then asserts the final plan is CLEAN: imports only, with any remaining changes
# limited to tags/tags_all (the provenance tags the import stamps). Any create /
# destroy / replace, or any non-tag attribute drift, fails the run — that's the
# "rock solid, clean tag-only plan" contract.
#
# This is the live tier (like `make test-roundtrip`): it needs real AWS creds,
# so it is operator-run, not a CI gate. Point it at any account you've authed
# into (cust1/cust2/cust3, ...). Multi-region is exercised by listing >1 region
# or REGIONS=all.
#
# Prereqs:
#   * live AWS creds — `aws sts get-caller-identity` must succeed
#   * a terraform binary on PATH (or set TERRAFORM_BINARY)
#   * an offline provider mirror (corp network blocks registry.terraform.io);
#     defaults to ~/.terraform.d/plugin-cache. Populate it once on a network
#     that can reach the registry: `terraform providers mirror <dir>` from any
#     stack pinning the aws provider, or copy from the mars image cache.
#
# Env knobs:
#   REVERSE_E2E_REGIONS   comma-separated regions, or "all"   (default: us-east-1)
#   REVERSE_E2E_PROJECT   project tag filter; "" = whole acct (default: "")
#   REVERSE_E2E_PRIMARY   primary region (globals fold here)  (default: 1st region / us-east-1)
#   REVERSE_E2E_MIRROR    filesystem_mirror dir               (default: ~/.terraform.d/plugin-cache)
#   REVERSE_E2E_OUTDIR    work/output dir                     (default: mktemp -d)
#   TERRAFORM_BINARY      terraform binary path               (default: PATH lookup)
#   REVERSE_E2E_KEEP      set to 1 to keep the work dir       (default: cleaned on success)
#
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

REGIONS="${REVERSE_E2E_REGIONS:-us-east-1}"
PROJECT="${REVERSE_E2E_PROJECT:-}"
MIRROR="${REVERSE_E2E_MIRROR:-$HOME/.terraform.d/plugin-cache}"
OUTDIR="${REVERSE_E2E_OUTDIR:-$(mktemp -d -t reverse-e2e.XXXXXX)}"
mkdir -p "$OUTDIR"
PRIMARY="${REVERSE_E2E_PRIMARY:-}"
if [[ -z "$PRIMARY" ]]; then
  if [[ "$REGIONS" == "all" ]]; then PRIMARY="us-east-1"; else PRIMARY="${REGIONS%%,*}"; fi
fi

log()  { printf '\033[1;36m[reverse-e2e]\033[0m %s\n' "$*"; }
fail() { printf '\033[1;31m[reverse-e2e] FAIL:\033[0m %s\n' "$*" >&2; exit 1; }

# ---- preflight ------------------------------------------------------------
command -v go >/dev/null || fail "go not on PATH"
TF_BIN="${TERRAFORM_BINARY:-$(command -v terraform || true)}"
[[ -n "$TF_BIN" ]] || fail "no terraform binary (install one or set TERRAFORM_BINARY)"
log "terraform: $TF_BIN ($("$TF_BIN" version | head -1))"

if ! aws sts get-caller-identity >/dev/null 2>&1; then
  fail "no live AWS creds (aws sts get-caller-identity failed). Re-auth (aws_login <role>) and retry."
fi
ACCT="$(aws sts get-caller-identity --query Account --output text 2>/dev/null || echo '?')"
log "AWS account: $ACCT  regions: $REGIONS  primary: $PRIMARY  project: '${PROJECT:-<whole-account>}'"

[[ -d "$MIRROR/registry.terraform.io/hashicorp/aws" ]] || \
  fail "offline mirror missing the aws provider at $MIRROR/registry.terraform.io/hashicorp/aws — populate it once (see header)."

# ---- offline provider mirror ---------------------------------------------
TFRC="$OUTDIR/reverse-e2e.tfrc"
cat >"$TFRC" <<EOF
provider_installation {
  filesystem_mirror {
    path    = "$MIRROR"
    include = ["registry.terraform.io/*/*"]
  }
  direct {
    exclude = ["registry.terraform.io/*/*"]
  }
}
EOF
export TF_CLI_CONFIG_FILE="$TFRC"
log "offline mirror wired via TF_CLI_CONFIG_FILE=$TFRC"

# ---- build the CLI --------------------------------------------------------
BIN="$OUTDIR/insideout-import"
log "building insideout-import..."
go build -o "$BIN" ./cmd/insideout-import

# ---- stage 2a: discover identities (--no-hcl: fast, reverse regenerates HCL)
DISC="$OUTDIR/discover"
mkdir -p "$DISC"
log "discover (identities only) across regions: $REGIONS..."
"$BIN" discover \
  --provider aws \
  --no-hcl \
  --regions "$REGIONS" \
  --project "$PROJECT" \
  --output-dir "$DISC"
[[ -f "$DISC/imported.json" ]] || fail "discover produced no imported.json"
NRES="$(python3 -c 'import json,sys;print(len(json.load(open(sys.argv[1]))))' "$DISC/imported.json" 2>/dev/null || echo '?')"
log "discovered $NRES importable resource(s)"

# ---- stages 2b/2c + final plan: the production engine ---------------------
OUT="$OUTDIR/out"
mkdir -p "$OUT"
log "reverse (genconfig → driftfix → depchase → terraform plan)..."
REV_ARGS=(reverse
  --input "$DISC/imported.json"
  --output-dir "$OUT"
  --region "$PRIMARY"
  --import-project-id "reverse-e2e"
  --import-session-id "reverse-e2e")
[[ -n "${TERRAFORM_BINARY:-}" ]] && REV_ARGS+=(--terraform-binary "$TERRAFORM_BINARY")
"$BIN" "${REV_ARGS[@]}"

# ---- assert: clean, tag-only plan -----------------------------------------
log "asserting clean tag-only plan..."
python3 - "$OUT" <<'PY'
import json, sys
out = sys.argv[1]
ps = json.load(open(f"{out}/plan-summary.json"))
plan = json.load(open(f"{out}/tfplan.json"))

def norm(v):
    # SIT-style null normalization: collapse empties so AWS-API response
    # normalization (null/[]/{}/""/0/false) doesn't read as drift.
    if v in (None, [], {}, "", 0, False):
        return None
    return v

problems = []

# 1) coarse counts: no creates, destroys, or replaces.
for k in ("add_count", "destroy_count", "replace_count"):
    if ps.get(k, 0):
        problems.append(f"plan-summary.{k} = {ps[k]} (want 0)")

# 2) per-resource: imports are no-ops; updates may touch ONLY tags/tags_all.
TAGS = {"tags", "tags_all"}
for rc in plan.get("resource_changes", []):
    actions = rc.get("change", {}).get("actions", [])
    addr = rc.get("address", "?")
    if "create" in actions or "delete" in actions:
        problems.append(f"{addr}: actions={actions} (create/delete/replace not allowed)")
        continue
    if actions == ["update"]:
        before = rc["change"].get("before") or {}
        after = rc["change"].get("after") or {}
        changed = {k for k in set(before) | set(after) if norm(before.get(k)) != norm(after.get(k))}
        extra = changed - TAGS
        if extra:
            problems.append(f"{addr}: non-tag changes {sorted(extra)}")

print(f"  plan-summary: import={ps.get('import_count')} add={ps.get('add_count')} "
      f"change={ps.get('change_count')} destroy={ps.get('destroy_count')} "
      f"replace={ps.get('replace_count')}")

if problems:
    print("\n  NOT a clean tag-only plan:")
    for p in problems:
        print(f"    - {p}")
    sys.exit(1)
print("  CLEAN: imports only, changes limited to tags/tags_all ✅")
PY

log "PASS — clean tag-only plan. Artifacts in $OUT"
if [[ "${REVERSE_E2E_KEEP:-0}" != "1" ]]; then
  rm -rf "$OUTDIR"
else
  log "kept work dir: $OUTDIR"
fi

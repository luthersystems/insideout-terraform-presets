#!/usr/bin/env bash
# Static analysis: every entry in a per-type `overrides:` map under
# cmd/enrichgen/<type>.go must be preceded by a comment that places it
# in one of nine allowed justification categories.
#
# Why this exists: overrides exist precisely because the engine's
# default reflection-driven mapping doesn't capture the per-field
# semantics the TF provider applies. The bug class this lint defends
# against (e.g. issue #415, commit 6985127's `enable_object_retention`
# Mode-gate fix) is "author wrote an override without checking what
# terraform-provider-google actually does for that field, so our
# generator's output diverges from real TF state on first import."
#
# The lint cannot verify that the author's claim is correct (no
# automated provider-comparison harness; see issue #415 for the
# investigation that ruled that out). But by requiring the author to
# place each override in a labelled justification category, it
# forces a deliberate moment of "why am I writing this override?"
# at PR-author time and surfaces the answer at PR-review time.
#
# Categories (any one token in the preceding comment block, or on the
# override key line itself, satisfies the lint):
#
#   1. Provider citation:    flattenXxx / setXxx / terraform-provider-google
#                            / "mirror the provider"
#   2. TF-only sentinel:     the literal token TF-only
#   3. Computed-only skip:   computed-only
#   4. Decision-doc ref:     decision #N (covers decision #5, decision #34, ...)
#   5. Caller-supplied:      caller
#   6. Block decoration:     block-
#
# Out of scope: wrapperIndirections and blockGates maps. Those are
# emit-or-skip and traversal-shape decisions, different bug class from
# the value-semantics drift overrides protect against.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

shopt -s nullglob
files=("$REPO_ROOT"/cmd/enrichgen/*.go)
if (( ${#files[@]} == 0 )); then
  echo "FAIL: no files matched cmd/enrichgen/*.go — has the generator moved?"
  exit 1
fi

echo "=== Checking cmd/enrichgen override entries for citation comments ==="
echo

any_fail=0

for f in "${files[@]}"; do
  # Skip files that have no overrides map at all (engine.go, registry.go,
  # main.go). awk handles them safely too, but skipping is faster.
  if ! grep -q 'overrides:[[:space:]]*map\[string\]override' "$f"; then
    continue
  fi

  awk '
    function matches_category(s,    re) {
      # Provider citation: function name like flattenBucketX, or prose
      # use of "flatten" / "flattened from", or an explicit terraform-
      # provider-google reference, or a "Mirror... provider" claim.
      if (s ~ /[fF]latten/) return 1
      if (s ~ /[sS]et[A-Z][a-z]/) return 1
      if (s ~ /terraform-provider-google/) return 1
      if (s ~ /[Mm]irror[^.]*provider/) return 1
      # TF-only sentinel.
      if (s ~ /TF-only/) return 1
      # Computed-only skip (decision #5).
      if (s ~ /[Cc]omputed-only/) return 1
      # Reference to a numbered decision doc (decision #5, decision-#34, ...).
      if (s ~ /[Dd]ecision[ -]#[0-9]+/) return 1
      # Caller-supplied (value comes from a parameter, not the API).
      if (s ~ /[Cc]aller/) return 1
      # Block decoration (field inside a nested block whose presence is
      # gated by the parent blockGate).
      if (s ~ /[Bb]lock-/) return 1
      return 0
    }

    function report(line_no, text, comm,    n, ls, i) {
      printf "%s:%d: ERROR: override entry lacks a citation comment\n", FILENAME, line_no
      printf "  override key line: %s\n", text
      if (comm != "") {
        n = split(comm, ls, "\n")
        printf "  preceding comment block (no allowed token):\n"
        for (i = 1; i <= n; i++) { printf "    %s\n", ls[i] }
      } else {
        printf "  no preceding comment block\n"
      }
      printf "  Add a comment containing one of:\n"
      printf "    - flattenXxx / setXxx / terraform-provider-google / \"mirror the provider\"\n"
      printf "    - TF-only\n"
      printf "    - computed-only\n"
      printf "    - decision #N\n"
      printf "    - caller\n"
      printf "    - block-\n"
      failed = 1
    }

    function check_entry(line_no, text, comm) {
      if (matches_category(comm)) return
      if (matches_category(text)) return
      report(line_no, text, comm)
    }

    BEGIN {
      failed = 0
      comment_block = ""
      pending = 0
      pending_line = 0
      pending_text = ""
      pending_comment = ""
      pending_count = 0
    }

    # Comment line: extend the current comment block.
    /^[[:space:]]*\/\// {
      if (comment_block == "") {
        comment_block = $0
      } else {
        comment_block = comment_block "\n" $0
      }
      next
    }

    # Blank line: drop the comment block (it does not extend across
    # blanks). Also abandon any pending classification — a key whose
    # body never reached snippet:/APIPath: is malformed; let the Go
    # compiler complain.
    /^[[:space:]]*$/ {
      comment_block = ""
      if (pending) { pending = 0 }
      next
    }

    # Override-key candidate: "Google<...>.<field>": {
    /^[[:space:]]*"Google[A-Za-z]+\.[a-z_]+":[[:space:]]*\{/ {
      key_line = NR
      key_text = $0
      if ($0 ~ /snippet:/) {
        # Single-line entry (e.g. {snippet: skip},). Check immediately.
        check_entry(key_line, key_text, comment_block)
        # Keep comment_block in scope for consecutive entries.
        next
      }
      if ($0 ~ /APIPath:/) {
        # Inline wrapperIndirection — not in scope for this lint.
        next
      }
      # Multi-line entry: defer the check until we know its kind.
      pending = 1
      pending_line = key_line
      pending_text = key_text
      pending_comment = comment_block
      pending_count = 0
      next
    }

    # While pending classification, peek subsequent lines for the
    # discriminating keyword. Give up after a small window.
    pending {
      pending_count++
      if ($0 ~ /snippet:/) {
        check_entry(pending_line, pending_text, pending_comment)
        pending = 0
        next
      }
      if ($0 ~ /APIPath:/) {
        pending = 0
        next
      }
      if (pending_count > 8) {
        # Not an override entry shape we recognize. Move on silently;
        # the lint is best-effort, not a parser.
        pending = 0
      }
      next
    }

    END { exit failed }
  ' "$f" || any_fail=1
done

if (( any_fail )); then
  echo
  echo "FAIL: One or more overrides in cmd/enrichgen/*.go lack a justification comment."
  echo "Fix: add a comment immediately above the override entry (or to the same"
  echo "logical comment block) that places it in one of the six categories above."
  echo "See tests/lint-enrichgen-override-citations.sh for the full rationale and"
  echo "issue #415 for why this lint exists in lieu of a provider-comparison harness."
  exit 1
fi

echo "PASS: All cmd/enrichgen overrides carry a citation comment."

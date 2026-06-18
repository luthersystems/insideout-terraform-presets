#!/usr/bin/env bash
#
# SessionStart hook for Claude Code (wired in .claude/settings.json).
#
# Runs on EVERY session -- local AND Claude Code on the web -- AFTER Claude launches,
# with the repository cloned. Kept light (it repeats each session, unlike the cached
# setup script). Cloud-only work is gated on CLAUDE_CODE_REMOTE so a developer's local
# machine is never touched. This script is committed identically in `reliable` and
# `insideout-terraform-presets`; behaviour adapts to which files the repo actually has.
#
set -uo pipefail

ROOT="${CLAUDE_PROJECT_DIR:-.}"

# Pre-warm Go modules so the first build/test is fast. Cheap locally; useful on a fresh
# cloud VM. Non-fatal.
if [ -f "$ROOT/go.mod" ] && command -v go >/dev/null 2>&1; then
  ( cd "$ROOT" && go mod download ) || true
fi

# --- Cloud-only -------------------------------------------------------------------
if [ "${CLAUDE_CODE_REMOTE:-}" = "true" ]; then
  # Secrets: the environment holds only a scoped 1Password service-account token
  # (OP_SERVICE_ACCOUNT_TOKEN). Resolve real secrets at runtime from the repo's
  # `op://` sources and write them to $CLAUDE_ENV_FILE so subsequent tool calls (incl.
  # the codex plugin) see them. Values never enter the persisted environment config.
  #   - .env.local.example: reliable's canonical dev env (all Reliable-Dev refs)
  #   - scripts/cloud-secrets.op.tpl: cloud-only extras (e.g. codex's OPENAI_API_KEY)
  if [ -n "${OP_SERVICE_ACCOUNT_TOKEN:-}" ] && command -v op >/dev/null 2>&1 && [ -n "${CLAUDE_ENV_FILE:-}" ]; then
    for src in "$ROOT/.env.local.example" "$ROOT/scripts/cloud-secrets.op.tpl"; do
      [ -f "$src" ] || continue
      # Strip comment lines first: op inject does raw text substitution and aborts the
      # whole file if it sees any "op:" token in a comment (e.g. .env.local.example's
      # explanatory comments). Comments carry no env vars, so dropping them is safe.
      if resolved=$(grep -vE '^[[:space:]]*#' "$src" | op inject 2>/tmp/op-inject.err); then
        # $CLAUDE_ENV_FILE is shell-sourced, so SINGLE-QUOTE every value: raw values can
        # contain &, spaces, $, etc. (e.g. POSTGRES_URL ends in ...&channel_binding=require,
        # whose unquoted & backgrounds the assignment and drops the var). Escape embedded
        # single quotes as '\''.
        printf '%s\n' "$resolved" | while IFS= read -r kv; do
          case "$kv" in
            *=*)
              k=${kv%%=*}; v=${kv#*=}; v=${v//\'/\'\\\'\'}
              printf "%s='%s'\n" "$k" "$v" >> "$CLAUDE_ENV_FILE"
              ;;
          esac
        done
        echo "[cloud-session-start] injected secrets from $(basename "$src")"
      else
        echo "[cloud-session-start] WARNING: op inject failed for $(basename "$src"): $(cat /tmp/op-inject.err 2>/dev/null)" >&2
      fi
    done
  else
    echo "[cloud-session-start] skipping secret injection (need OP_SERVICE_ACCOUNT_TOKEN + op CLI + CLAUDE_ENV_FILE)" >&2
  fi

  # Pull Git LFS objects (reliable's go tests read an LFS-tracked tokenizer model).
  if command -v git-lfs >/dev/null 2>&1 && [ -f "$ROOT/.gitattributes" ] && grep -q 'filter=lfs' "$ROOT/.gitattributes" 2>/dev/null; then
    ( cd "$ROOT" && git lfs pull ) || echo "[cloud-session-start] WARNING: git lfs pull failed" >&2
  fi

  # Force codex to API-key auth (it reads the injected OPENAI_API_KEY). The headless
  # sandbox has no interactive `codex login` / ~/.codex/auth.json. Never written locally.
  mkdir -p "${HOME}/.codex"
  if ! grep -q '^preferred_auth_method' "${HOME}/.codex/config.toml" 2>/dev/null; then
    printf 'preferred_auth_method = "apikey"\n' >> "${HOME}/.codex/config.toml"
  fi
fi

exit 0

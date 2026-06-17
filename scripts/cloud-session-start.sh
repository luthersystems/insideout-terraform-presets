#!/usr/bin/env bash
#
# SessionStart hook for Claude Code (wired in .claude/settings.json).
#
# Runs on EVERY session -- local AND Claude Code on the web -- AFTER Claude launches,
# with the repository already cloned. Unlike the cached setup script, this repeats each
# session, so keep it light. Cloud-only behaviour is gated on CLAUDE_CODE_REMOTE so it
# never touches a local developer's machine.
#
set -uo pipefail

# Pre-warm the Go module cache so the first `go build` / `go test -race ./...` is fast.
# Fresh cloud VM has no module cache; locally this is a no-op. Non-fatal.
if command -v go >/dev/null 2>&1; then
  go mod download || true
fi

# --- Cloud-only: pull secrets from 1Password into the session env -----------------
# The cloud environment holds ONLY a scoped 1Password service-account token
# (OP_SERVICE_ACCOUNT_TOKEN, set in the web UI env-vars field). Real secrets live in
# 1Password and are resolved here at runtime. `.claude/cloud-secrets.env.tpl` lists
# which vars to pull as `op://` references (no secrets committed). `op inject` resolves
# them; writing to $CLAUDE_ENV_FILE exposes them to subsequent tool calls (e.g. the
# codex plugin), so the values never enter the persisted environment config.
if [ "${CLAUDE_CODE_REMOTE:-}" = "true" ]; then
  tpl="${CLAUDE_PROJECT_DIR:-.}/scripts/cloud-secrets.op.tpl"
  if [ -n "${OP_SERVICE_ACCOUNT_TOKEN:-}" ] && command -v op >/dev/null 2>&1 && [ -f "$tpl" ]; then
    if [ -n "${CLAUDE_ENV_FILE:-}" ]; then
      # Resolve op:// refs to KEY=value lines and append to the session env file.
      if op inject -i "$tpl" >> "$CLAUDE_ENV_FILE" 2>/tmp/op-inject.err; then
        echo "[cloud-session-start] injected secrets from 1Password into session env"
      else
        echo "[cloud-session-start] WARNING: op inject failed: $(cat /tmp/op-inject.err 2>/dev/null)" >&2
      fi
    fi
  else
    echo "[cloud-session-start] skipping secret injection (need OP_SERVICE_ACCOUNT_TOKEN, the op CLI, and $tpl)" >&2
  fi

  # Force the codex CLI to use API-key auth (it reads OPENAI_API_KEY, now injected
  # above). The headless sandbox has no interactive `codex login` / ~/.codex/auth.json.
  # Never written locally, so a developer's existing codex login is untouched.
  mkdir -p "${HOME}/.codex"
  if ! grep -q '^preferred_auth_method' "${HOME}/.codex/config.toml" 2>/dev/null; then
    printf 'preferred_auth_method = "apikey"\n' >> "${HOME}/.codex/config.toml"
  fi
fi

exit 0

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
# A fresh cloud VM has no module cache; locally this is a no-op. Non-fatal.
if command -v go >/dev/null 2>&1; then
  go mod download || true
fi

if [ "${CLAUDE_CODE_REMOTE:-}" = "true" ]; then
  # Cloud only: force the codex CLI to use API-key auth. The sandbox is headless, so
  # there is no interactive `codex login` and no ~/.codex/auth.json; codex picks up the
  # OPENAI_API_KEY env var (set in the environment's "Environment variables" field).
  # Writing preferred_auth_method makes that choice explicit/defensive. We never write
  # this locally, so a developer's existing `codex login` is untouched.
  mkdir -p "${HOME}/.codex"
  if ! grep -q '^preferred_auth_method' "${HOME}/.codex/config.toml" 2>/dev/null; then
    printf 'preferred_auth_method = "apikey"\n' >> "${HOME}/.codex/config.toml"
  fi
fi

exit 0

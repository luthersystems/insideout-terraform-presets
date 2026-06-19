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

  # Install JS deps so the frontend just works on a fresh cloud VM. node_modules is NOT in
  # the cached setup-script snapshot (that captures /opt + apt, not the repo tree), so every
  # fresh session starts without it. `make dev` then runs `npx next dev`, which -- with no
  # local install -- silently fetches a THROWAWAY Next.js into a temp dir and dies with a
  # Turbopack "couldn't find next/package.json / workspace root" error, so :3000 serves
  # nothing (the Go API on :3001 is unaffected). A frozen-lockfile install fixes it and also
  # unblocks Vitest + mock-ui. This is the documented best practice: project setup like
  # `npm install` belongs in a SessionStart hook, not the cloud setup script
  # (https://code.claude.com/docs/en/claude-code-on-the-web#setup-scripts-vs-sessionstart-hooks).
  # Idempotent: skip when node_modules already exists (resumed session) to avoid the reinstall
  # cost. Non-fatal. Guarded on package-lock.json so it's a no-op in presets (this hook is
  # committed identically in both repos).
  if [ -f "$ROOT/package-lock.json" ] && [ ! -d "$ROOT/node_modules" ] && command -v npm >/dev/null 2>&1; then
    echo "[cloud-session-start] npm ci (installing JS deps for make dev / vitest / mock-ui)"
    ( cd "$ROOT" && npm ci ) || echo "[cloud-session-start] WARNING: npm ci failed; run it manually before make dev" >&2
  fi

  # Warm the Go BUILD cache in the background so the first `make dev` / `go test` compile is
  # fast. `go mod download` (above) only fetches module *sources*; nothing is compiled, and
  # GOCACHE (~/.cache/go-build) is NOT in the cached snapshot, so a fresh session otherwise
  # compiles the entire dependency graph on the first build. Backgrounded + best-effort so it
  # never delays session readiness and overlaps the user picking up the ticket; Go's build
  # cache is concurrency-safe, so it can run alongside a user-triggered build. A blocking
  # warm would be pointless (it'd just move the same compile cost to boot). Cloud-only.
  if [ -f "$ROOT/go.mod" ] && command -v go >/dev/null 2>&1; then
    echo "[cloud-session-start] warming Go build cache in background (go build ./...)"
    ( cd "$ROOT" && go build ./... >/tmp/go-build-warm.log 2>&1 ) &
  fi

  # Postgres: the Go backend uses pgx over raw TCP, which can't egress the proxy. If
  # POSTGRES_URL points at Neon, auto-enable the WebSocket transport (internal/pgws) by
  # deriving wss://<host>/v2. No-op until the pgws change is merged (it only sets an env
  # var nothing reads yet); honored once it is. Local/prod never set this, so unaffected.
  if [ -z "${PG_WS_PROXY_URL:-}" ] && [ -n "${CLAUDE_ENV_FILE:-}" ]; then
    pghost=$(grep -m1 '^POSTGRES_URL=' "$CLAUDE_ENV_FILE" 2>/dev/null \
      | sed -E "s/^POSTGRES_URL='?[a-z]+:\/\/[^@]*@([^:\/?']+).*/\1/")
    case "$pghost" in
      *.neon.tech)
        printf "PG_WS_PROXY_URL='wss://%s/v2'\n" "$pghost" >> "$CLAUDE_ENV_FILE"
        echo "[cloud-session-start] Postgres: WebSocket transport via ${pghost}/v2"
        ;;
    esac
  fi

  # Redis: raw-TCP RESP can't egress and Upstash has no proxy-traversable RESP endpoint.
  # Blank REDIS_URL so the app uses its in-memory bus (the correct, divergence-free
  # behavior for a single dev VM) instead of hanging on the unreachable remote. Real
  # cross-instance pub/sub needs RESP/TCP and is a prod-only concern; prod is unaffected.
  if [ -n "${CLAUDE_ENV_FILE:-}" ]; then
    printf "REDIS_URL=''\nUPSTASH_REDIS_URL=''\n" >> "$CLAUDE_ENV_FILE"
    echo "[cloud-session-start] Redis: in-memory bus (remote Upstash unreachable over the proxy)"
  fi

  # Plugin install. The marketplaces named in .claude/settings.json's extraKnownMarketplaces
  # get cloned at launch, but `enabledPlugins` only ENABLES an already-installed plugin -- it
  # does NOT install one. On the ephemeral cloud VM nothing ever runs the install, so
  # installed_plugins.json stays empty and the plugins' skills/agents silently never load.
  # Install each enabled plugin explicitly; `claude plugin install` is idempotent (no-op if
  # already installed) and handles both local-subdir sources (codex, claude-config) and
  # external-git sources (firecrawl). Driven off settings.json so it stays in sync with
  # enabledPlugins. Keep this hook identical in insideout-terraform-presets.
  SETTINGS="$ROOT/.claude/settings.json"
  if command -v claude >/dev/null 2>&1 && [ -f "$SETTINGS" ] && command -v node >/dev/null 2>&1; then
    node -e 'const s=require(process.argv[1]);for(const k of Object.keys(s.enabledPlugins||{}))if(s.enabledPlugins[k])console.log(k)' "$SETTINGS" 2>/dev/null \
    | while IFS= read -r plugin; do
        [ -n "$plugin" ] || continue
        claude plugin install "$plugin" >/dev/null 2>&1 \
          && echo "[cloud-session-start] plugin installed: $plugin" \
          || echo "[cloud-session-start] WARNING: plugin install failed: $plugin" >&2
      done
  fi

  # Codex auth = ChatGPT subscription (flat-rate), NOT the metered platform API key. The
  # headless sandbox can't run the interactive `codex login` OAuth flow, so transplant the
  # OAuth credential: a `codex login` run on a workstation writes ~/.codex/auth.json
  # (auth_mode "chatgpt" + a long-lived refresh token); it is stored as a 1Password document
  # (Reliable-Dev/codex-auth-json/auth.json) and dropped in here. Re-run `codex login` locally
  # and re-upload when the refresh token rotates.
  mkdir -p "${HOME}/.codex"
  printf 'preferred_auth_method = "chatgpt"\n' > "${HOME}/.codex/config.toml"
  if [ -n "${OP_SERVICE_ACCOUNT_TOKEN:-}" ] && command -v op >/dev/null 2>&1; then
    if op read "op://Reliable-Dev/codex-auth-json/auth.json" > "${HOME}/.codex/auth.json" 2>/tmp/codex-auth.err; then
      chmod 600 "${HOME}/.codex/auth.json"
      echo "[cloud-session-start] codex: installed ChatGPT-subscription auth.json"
      # Warm codex in the background. The FIRST call on a fresh VM cold-starts: refresh the
      # OAuth token, fetch the models list, and open a websocket to
      # chatgpt.com/backend-api/codex/responses. That cold websocket intermittently stalls
      # through the sandbox egress proxy (the handshake completes but response frames don't
      # flow); priming it here -- time-boxed and best-effort -- populates the models cache and
      # refreshes the token so the user's first real `codex review` / `codex task` is reliable
      # (warm runs verified 3/3). Backgrounded so it never delays session readiness.
      if command -v codex >/dev/null 2>&1; then
        ( cd "$ROOT" && timeout 75 codex exec --skip-git-repo-check "ok" >/tmp/codex-warm.log 2>&1; \
          echo "[cloud-session-start] codex warmup exited $?" >>/tmp/codex-warm.log ) &
      fi
    else
      echo "[cloud-session-start] WARNING: codex auth.json fetch failed: $(cat /tmp/codex-auth.err 2>/dev/null)" >&2
    fi
  else
    echo "[cloud-session-start] skipping codex auth (need OP_SERVICE_ACCOUNT_TOKEN + op CLI)" >&2
  fi
fi

exit 0

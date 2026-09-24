#!/usr/bin/env bash
#
# Claude Code on the web -- SHARED environment "Setup script".
#
# This is the union of the system tools needed by both `reliable` and
# `insideout-terraform-presets`, so a single Claude Code on the web environment can be
# reused across both repos. Copy the full contents into the "Setup script" field of the
# shared environment:
#     claude.ai/code -> New session -> cloud icon -> Add/Edit environment -> Setup script.
#
# UPSTREAM: luthersystems/reliable scripts/cloud-web-setup.sh is the canonical copy (and the
# one to paste into the shared environment -- it is a superset of this file). This copy is
# derived from it. Last synced with reliable origin/main on 2026-09-24.
# When syncing, take reliable's generic fixes (tool version bumps, apt/dpkg hardening,
# idempotency fixes) and keep these intentional differences:
#   - GO_VERSION follows THIS repo's go.mod directive, not reliable's CI toolchain.
#   - reliable-only extras are omitted here: air (reliable `make dev`), agent-browser
#     (reliable UI validation), and the AWS CLI / kubectl / gcloud ops-troubleshooting CLIs.
# Never lower a version this file pins higher than reliable does.
#
# It runs ONCE as root on a fresh Ubuntu 24.04 VM and its filesystem output is CACHED
# (~7-day expiry), so it only re-runs when you edit it. Keep it to repo-independent
# system tools only -- the repo is NOT reliably present here. Repo-specific warmup +
# secret fetching live in scripts/cloud-session-start.sh (a SessionStart hook).
#
# Preinstalled in the sandbox (do NOT install): Node, Go, Docker, Postgres, Redis.
# Secrets are NOT handled here -- the only credential in the environment is a scoped
# 1Password service-account token (OP_SERVICE_ACCOUNT_TOKEN); see docs/CLAUDE_CODE_WEB.md.
#
set -Eeuo pipefail

# Persistent setup log. The cached setup phase's stdout is not easily inspectable after the
# fact, so mirror every log line (UTC-stamped) to a file that survives into the session
# snapshot. Inspect from any session with:  cat /var/log/cloud-web-setup.log
# Override the path with CLOUD_WEB_SETUP_LOG; falls back to /tmp if /var/log isn't writable.
LOG_FILE="${CLOUD_WEB_SETUP_LOG:-/var/log/cloud-web-setup.log}"
mkdir -p "$(dirname "$LOG_FILE")" 2>/dev/null || true
: > "$LOG_FILE" 2>/dev/null || LOG_FILE=/tmp/cloud-web-setup.log
: > "$LOG_FILE" 2>/dev/null || true

TERRAFORM_VERSION=1.7.5     # presets CI pin (hashicorp/setup-terraform)
GOLANGCI_VERSION=v2.13.2    # fleet pin (reliable CI golangci-lint-action)
GO_VERSION=1.25.8           # this repo's go.mod directive (CI uses go-version-file: go.mod)
ARCH="$(dpkg --print-architecture)"   # auto-detect: amd64 or arm64

log() {
  local line="[cloud-web-setup] $*"
  echo "$line"
  # O_APPEND keeps these short lines atomic across the parallel-phase subshells.
  printf '%s %s\n' "$(date -u +%FT%TZ)" "$line" >> "$LOG_FILE" 2>/dev/null || true
}

# Record any unhandled failure (a `set -e` abort) with its line number, so a step that dies
# in the cached phase leaves a breadcrumb in the log instead of vanishing silently. The
# best-effort steps are already guarded with `|| log ...` / `|| true`, so they won't trip this.
trap 'rc=$?; log "ERROR: setup aborted at line ${LINENO} (exit ${rc})"; exit ${rc}' ERR

install_terraform() {
  log "installing terraform ${TERRAFORM_VERSION}"
  curl -fsSL "https://releases.hashicorp.com/terraform/${TERRAFORM_VERSION}/terraform_${TERRAFORM_VERSION}_linux_${ARCH}.zip" -o /tmp/terraform.zip
  unzip -o /tmp/terraform.zip -d /usr/local/bin
  terraform version
}

install_tflint() {
  log "installing tflint (plugins fetched lazily via 'tflint --init')"
  curl -fsSL https://raw.githubusercontent.com/terraform-linters/tflint/master/install_linux.sh | bash
  tflint --version
}

install_golangci() {
  log "installing golangci-lint ${GOLANGCI_VERSION} (reliable .golangci.yml is v2)"
  curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh \
    | sh -s -- -b /usr/local/bin "${GOLANGCI_VERSION}"
  golangci-lint --version
}

install_gopls() {
  # Go language server for Claude Code's LSP (diagnostics, hover, go-to-def). Not part of
  # the Go toolchain, so it must be installed separately. GOBIN puts it on /usr/local/bin
  # (on PATH). Best-effort: a gopls hiccup shouldn't poison the whole cache.
  log "installing gopls (Go LSP)"
  GOBIN=/usr/local/bin go install golang.org/x/tools/gopls@latest && gopls version \
    || log "gopls install failed (non-fatal; Go LSP unavailable)"
}

install_npm_clis() {
  # codex backs the `codex` plugin; firecrawl-cli backs the `firecrawl` plugin; vercel
  # for reliable's vercel-* targets. node/npm are preinstalled.
  log "installing global npm CLIs: @openai/codex, firecrawl-cli, vercel"
  npm install -g @openai/codex firecrawl-cli vercel
  codex --version
  vercel --version
}

install_codex_auth_and_plugins() {
  # Codex auth + Claude plugins, baked into the cached snapshot.
  #
  # WHY HERE and not scripts/cloud-session-start.sh: the project's SessionStart hook
  # (.claude/settings.json) does NOT run in the Claude-Code-on-the-web / remote-execution
  # sandbox -- only the harness's own launcher hooks do (verified: ~/.claude.json carries no
  # trusted-project entry and the harness launcher-settings.json wires only git-identity). So
  # the codex auth + plugin install that live in that hook never execute on a fresh session:
  # codex 401s (no ~/.codex/auth.json) and `/codex:review` is missing (no
  # installed_plugins.json). This setup script's filesystem output IS snapshotted, so doing it
  # here makes the result present at the start of every new session with no hook required.
  # All best-effort: this runs under `set -e`, so each step is guarded to never poison the cache.

  # Codex ChatGPT-subscription auth (flat-rate). auth.json is a 1Password document (a
  # `codex login` on a workstation, re-uploaded when its refresh token rotates).
  if [ -n "${OP_SERVICE_ACCOUNT_TOKEN:-}" ] && command -v op >/dev/null 2>&1; then
    mkdir -p "${HOME}/.codex"
    printf 'preferred_auth_method = "chatgpt"\n' > "${HOME}/.codex/config.toml"
    if op read "op://Reliable-Dev/codex-auth-json/auth.json" > "${HOME}/.codex/auth.json" 2>/dev/null; then
      chmod 600 "${HOME}/.codex/auth.json"
      log "codex: installed ChatGPT-subscription auth.json (snapshot)"
    else
      log "WARNING: codex auth.json fetch failed (non-fatal)"
    fi
  else
    log "OP_SERVICE_ACCOUNT_TOKEN / op missing -- skipping codex auth"
  fi

  # Claude Code plugins: codex (=> /codex:review), claude-config (=> qa-professor / pr),
  # firecrawl. Best-effort: `claude` is a harness binary that may not be on PATH during the
  # cached setup phase. When it is, this bakes installed_plugins.json into the snapshot so the
  # plugins load at session start; when it isn't, this is a no-op and `codex review` from the
  # CLI (authed above) still works.
  if command -v claude >/dev/null 2>&1; then
    claude plugin marketplace add openai/codex-plugin-cc      >/dev/null 2>&1 || true
    claude plugin marketplace add sam-at-luther/claude-config >/dev/null 2>&1 || true
    for p in codex@openai-codex claude-config@claude-config firecrawl@claude-plugins-official; do
      if claude plugin install "$p" >/dev/null 2>&1; then
        log "plugin installed: $p"
      else
        log "WARNING: plugin install failed: $p (non-fatal; claude CLI may be unavailable at setup)"
      fi
    done
  else
    log "claude CLI not on PATH at setup -- skipping plugin install (codex CLI auth above still applies)"
  fi
}

install_playwright_deps() {
  # OS libraries for Playwright's chromium (root/apt). The browser BINARY is fetched
  # per-session in the hook. Best-effort: only reliable's `make test-mock` needs it.
  log "installing Playwright chromium OS deps (best-effort)"
  npx --yes playwright@1.58.2 install-deps chromium || log "playwright install-deps skipped/failed (non-fatal)"
}

# gh + 1Password CLI both use apt -> ONE transaction (no parallel dpkg lock contention).
install_apt_extras() {
  export DEBIAN_FRONTEND=noninteractive
  log "configuring gh apt repo"
  install -m 0755 -d /etc/apt/keyrings
  curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
    | tee /etc/apt/keyrings/githubcli-archive-keyring.gpg >/dev/null
  chmod go+r /etc/apt/keyrings/githubcli-archive-keyring.gpg
  echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" \
    > /etc/apt/sources.list.d/github-cli.list

  log "configuring 1Password CLI apt repo"
  curl -fsSL https://downloads.1password.com/linux/keys/1password.asc \
    | gpg --batch --yes --dearmor --output /usr/share/keyrings/1password-archive-keyring.gpg
  echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/1password-archive-keyring.gpg] https://downloads.1password.com/linux/debian/$(dpkg --print-architecture) stable main" \
    > /etc/apt/sources.list.d/1password.list
  mkdir -p /etc/debsig/policies/AC2D62742012EA22/
  curl -fsSL https://downloads.1password.com/linux/debian/debsig/1password.pol \
    -o /etc/debsig/policies/AC2D62742012EA22/1password.pol
  mkdir -p /usr/share/debsig/keyrings/AC2D62742012EA22
  curl -fsSL https://downloads.1password.com/linux/keys/1password.asc \
    | gpg --batch --yes --dearmor --output /usr/share/debsig/keyrings/AC2D62742012EA22/debsig.gpg

  log "installing gh + 1password-cli"
  apt-get update -y --allow-releaseinfo-change   # tolerate PPA Release-metadata changes (see phase 1)
  apt-get install -y gh 1password-cli
  gh --version
  op --version
}

ensure_go() {
  if command -v go >/dev/null 2>&1; then
    have="$(go version | awk '{print $3}' | sed 's/^go//')"
    want="${GO_VERSION%.*}"
    if [ "$(printf '%s\n%s\n' "$want" "$have" | sort -V | head -1)" = "$want" ]; then
      log "preinstalled go ${have} satisfies go.mod (>= ${want}); skipping"
      return 0
    fi
    log "preinstalled go ${have} older than ${want}; installing ${GO_VERSION}"
  else
    log "go not found; installing ${GO_VERSION}"
  fi
  curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${ARCH}.tar.gz" -o /tmp/go.tgz
  rm -rf /usr/local/go && tar -C /usr/local -xzf /tmp/go.tgz
  ln -sf /usr/local/go/bin/go /usr/local/bin/go
  ln -sf /usr/local/go/bin/gofmt /usr/local/bin/gofmt
  go version
}

# Phase 0 (apt hardening): on a fresh Ubuntu 24.04 VM the boot-time apt-daily /
# unattended-upgrades timers grab /var/lib/dpkg/lock-frontend a minute or two after boot --
# i.e. right in the gap between phase 1's apt and install_apt_extras' apt. An `apt-get install`
# with no lock timeout then dies instantly with "Could not get lock ... (exit 100)" -- an
# intermittent "setup aborted at line N" failure. Two defenses, both before any apt-get runs:
#   1. A global apt.conf.d drop-in so EVERY apt-get in this script (incl. the one buried inside
#      `playwright install-deps`) WAITS up to 10 min for the lock instead of failing immediately.
#   2. Best-effort stop of the background timers so they don't contend at all. No-op (|| true)
#      where systemd isn't the init (e.g. a container sandbox), where the conf drop-in is the
#      real safety net.
log "hardening apt against dpkg-lock contention (DPkg::Lock::Timeout + stop apt-daily timers)"
mkdir -p /etc/apt/apt.conf.d
printf 'DPkg::Lock::Timeout "600";\n' > /etc/apt/apt.conf.d/99-cloud-web-setup
systemctl stop apt-daily.timer apt-daily-upgrade.timer       >/dev/null 2>&1 || true
systemctl stop apt-daily.service apt-daily-upgrade.service   >/dev/null 2>&1 || true

# Phase 1 (sync): base packages the parallel installers depend on. git-lfs is needed by
# reliable's go tests (LFS-tracked tokenizer model); 'git lfs install' is system-wide here.
# bubblewrap is codex's sandbox runtime -- without it on PATH codex warns on every invocation
# and falls back to its bundled copy. rsync drives reliable's `make export-mock` /
# `make verify-mock` (mock-ui/scripts/export-mock.sh rsyncs the shared component tree into the
# insideout-app-mock export); without it that target dies with "rsync: command not found".
# The setup script is shared with reliable, so rsync ships here too even though presets has no
# mock-ui. Installed in this single synchronous transaction (not the parallel phase) to avoid
# dpkg-lock contention with install_apt_extras.
log "installing base apt packages (incl. git-lfs, bubblewrap, rsync)"
export DEBIAN_FRONTEND=noninteractive
# --allow-releaseinfo-change: the base image pre-configures PPAs this repo never installs from
# (deadsnakes, ondrej/php). When one of them changes its Release metadata -- e.g. ondrej/php
# flipped its Label from "PPA for PHP" to "Use packages.sury.org/php instead" -- a bare
# `apt-get update` aborts with exit 100 ("changed its 'Label' value ..."). The flag accepts the
# metadata change and proceeds; safe here because we pull nothing from those PPAs.
apt-get update -y --allow-releaseinfo-change
apt-get install -y --no-install-recommends unzip jq ca-certificates curl gnupg git-lfs bubblewrap rsync
git lfs install --system || true

# Phase 2 (parallel): non-apt installers PLUS the single apt user (install_apt_extras).
# install_playwright_deps is intentionally NOT here -- it also runs apt (install-deps),
# and two concurrent apt processes deadlock on the dpkg lock. It runs in phase 3.
pids=()
install_terraform        & pids+=("$!")
install_tflint           & pids+=("$!")
install_golangci         & pids+=("$!")
install_npm_clis         & pids+=("$!")
ensure_go                & pids+=("$!")
install_apt_extras       & pids+=("$!")

fail=0
for pid in "${pids[@]}"; do wait "$pid" || fail=1; done
if [ "$fail" -ne 0 ]; then
  log "ERROR: a required installer failed (see above); failing so the cache is not poisoned"
  exit 1
fi

# Phase 3 (sequential): runs after phase 2 so it doesn't race the dpkg lock (playwright)
# and so the Go toolchain is guaranteed present (gopls). Both best-effort.
install_playwright_deps || true
install_gopls
install_codex_auth_and_plugins || log "codex auth / plugin setup failed (non-fatal)"

# Docker is preinstalled but its daemon won't be running in a fresh session VM. If a task
# needs it, start it on demand: `sudo service docker start`. Not required for the standard
# build/lint/test flow (Postgres is reached via POSTGRES_URL).
log "setup complete: terraform, tflint, golangci-lint, gopls, go, codex, firecrawl-cli, vercel, op, gh, git-lfs, bubblewrap, rsync installed; codex auth + plugins baked"
# Explicit completion marker. If this line is ABSENT from the log, the script aborted partway
# (look for the "ERROR: setup aborted at line N" breadcrumb above). To check what was skipped
# vs failed, grep the log for: 'skipping plugin install' | 'plugin install failed' | 'WARNING' | 'ERROR'
log "OK: cloud-web-setup finished cleanly -- full log at ${LOG_FILE}"

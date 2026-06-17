#!/usr/bin/env bash
#
# Claude Code on the web -- SHARED environment "Setup script".
#
# This is the union of the system tools needed by both `reliable` and
# `insideout-terraform-presets`, so a single Claude Code on the web environment can be
# reused across both repos. This file is committed IDENTICALLY in both repos; copy the
# full contents into the "Setup script" field of the shared environment:
#     claude.ai/code -> New session -> cloud icon -> Add/Edit environment -> Setup script.
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
set -euo pipefail

TERRAFORM_VERSION=1.7.5     # presets CI pin (hashicorp/setup-terraform)
GOLANGCI_VERSION=v2.6.2     # reliable CI pin (golangci-lint-action)
GO_VERSION=1.25.0           # both repos' go.mod
ARCH=amd64                  # cloud sandbox is x86_64

log() { echo "[cloud-web-setup] $*"; }

install_terraform() {
  log "installing terraform ${TERRAFORM_VERSION}"
  curl -fsSL "https://releases.hashicorp.com/terraform/${TERRAFORM_VERSION}/terraform_${TERRAFORM_VERSION}_linux_${ARCH}.zip" -o /tmp/terraform.zip
  unzip -o /tmp/terraform.zip -d /usr/local/bin
  terraform version | head -1
}

install_tflint() {
  log "installing tflint (plugins fetched lazily via 'tflint --init')"
  curl -fsSL https://raw.githubusercontent.com/terraform-linters/tflint/master/install_linux.sh | bash
  tflint --version | head -1
}

install_golangci() {
  log "installing golangci-lint ${GOLANGCI_VERSION} (reliable .golangci.yml is v2)"
  curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh \
    | sh -s -- -b /usr/local/bin "${GOLANGCI_VERSION}"
  golangci-lint --version
}

install_npm_clis() {
  # codex backs the Claude Code `codex` plugin; vercel for reliable's vercel-* targets.
  # node/npm are preinstalled.
  log "installing global npm CLIs: @openai/codex, vercel"
  npm install -g @openai/codex vercel
  codex --version
  vercel --version
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
    | gpg --dearmor --output /usr/share/keyrings/1password-archive-keyring.gpg
  echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/1password-archive-keyring.gpg] https://downloads.1password.com/linux/debian/$(dpkg --print-architecture) stable main" \
    > /etc/apt/sources.list.d/1password.list
  mkdir -p /etc/debsig/policies/AC2D62742012EA22/
  curl -fsSL https://downloads.1password.com/linux/debian/debsig/1password.pol \
    -o /etc/debsig/policies/AC2D62742012EA22/1password.pol
  mkdir -p /usr/share/debsig/keyrings/AC2D62742012EA22
  curl -fsSL https://downloads.1password.com/linux/keys/1password.asc \
    | gpg --dearmor --output /usr/share/debsig/keyrings/AC2D62742012EA22/debsig.gpg

  log "installing gh + 1password-cli"
  apt-get update -y
  apt-get install -y gh 1password-cli
  gh --version | head -1
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

# Phase 1 (sync): base packages the parallel installers depend on. git-lfs is needed by
# reliable's go tests (LFS-tracked tokenizer model); 'git lfs install' is system-wide here.
log "installing base apt packages (incl. git-lfs)"
export DEBIAN_FRONTEND=noninteractive
apt-get update -y
apt-get install -y --no-install-recommends unzip jq ca-certificates curl gnupg git-lfs
git lfs install --system || true

# Phase 2 (parallel): independent installers. install_apt_extras is the only apt user.
pids=()
install_terraform        & pids+=("$!")
install_tflint           & pids+=("$!")
install_golangci         & pids+=("$!")
install_npm_clis         & pids+=("$!")
install_playwright_deps  & pids+=("$!")
ensure_go                & pids+=("$!")
install_apt_extras       & pids+=("$!")

fail=0
for pid in "${pids[@]}"; do wait "$pid" || fail=1; done
if [ "$fail" -ne 0 ]; then
  log "ERROR: a required installer failed (see above); failing so the cache is not poisoned"
  exit 1
fi

# Docker is preinstalled but its daemon won't be running in a fresh session VM. If a task
# needs it, start it on demand: `sudo service docker start`. Not required for the standard
# build/lint/test flow (Postgres is reached via POSTGRES_URL).
log "setup complete: $(terraform version | head -1), tflint $(tflint --version|head -1), $(golangci-lint --version), go $(go version|awk '{print $3}'), codex $(codex --version 2>/dev/null|head -1), vercel $(vercel --version 2>/dev/null), op $(op --version), gh $(gh --version|head -1)"

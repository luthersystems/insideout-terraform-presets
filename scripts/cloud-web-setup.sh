#!/usr/bin/env bash
#
# Claude Code on the web -- environment "Setup script" for insideout-terraform-presets.
#
# HOW TO USE: copy the FULL contents of this file into the "Setup script" field of
# your Claude Code on the web environment:
#     claude.ai/code -> New session -> click the cloud icon (environment name)
#     -> Add environment (or the gear to edit) -> "Setup script".
#
# It runs ONCE as root on a fresh Ubuntu 24.04 VM and its filesystem output is
# CACHED (~7-day expiry), so it only re-runs when you edit it. Keep it to
# repo-independent system tools only -- the repo is NOT reliably present here.
# Repo-specific warmup + secret fetching live in scripts/cloud-session-start.sh,
# wired as a SessionStart hook in .claude/settings.json.
#
# Secrets are NOT handled here. The only credential in the cloud environment is a
# scoped 1Password service-account token (OP_SERVICE_ACCOUNT_TOKEN, set in the
# environment's "Environment variables" field). The session hook uses it + the `op`
# CLI installed below to pull real secrets at runtime. See docs/CLAUDE_CODE_WEB.md.
#
# Pins mirror .github/workflows/terraform-validate.yml.
#
set -euo pipefail

TERRAFORM_VERSION=1.7.5   # matches CI: hashicorp/setup-terraform terraform_version
GO_VERSION=1.25.0         # matches go.mod `go 1.25.0`
ARCH=amd64                # cloud sandbox is x86_64

log() { echo "[cloud-web-setup] $*"; }

install_terraform() {
  log "installing terraform ${TERRAFORM_VERSION}"
  curl -fsSL "https://releases.hashicorp.com/terraform/${TERRAFORM_VERSION}/terraform_${TERRAFORM_VERSION}_linux_${ARCH}.zip" -o /tmp/terraform.zip
  unzip -o /tmp/terraform.zip -d /usr/local/bin
  /usr/local/bin/terraform version
}

install_tflint() {
  log "installing tflint (plugins fetched lazily via 'tflint --init')"
  curl -fsSL https://raw.githubusercontent.com/terraform-linters/tflint/master/install_linux.sh | bash
  tflint --version
}

install_codex() {
  # Codex CLI backs the `codex` Claude Code plugin (enabled in .claude/settings.json).
  # node/npm are preinstalled; a global install puts `codex` on PATH for the session user.
  log "installing OpenAI codex CLI (@openai/codex)"
  npm install -g @openai/codex
  codex --version
}

# gh + the 1Password CLI both use apt, so they share ONE apt transaction (no parallel
# dpkg lock contention). This whole function is run as a single background job.
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
    want="${GO_VERSION%.*}"  # major.minor
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

# Phase 1 (sync): base packages the parallel installers depend on.
log "installing base apt packages"
export DEBIAN_FRONTEND=noninteractive
apt-get update -y
apt-get install -y --no-install-recommends unzip jq ca-certificates curl gnupg

# Phase 2 (parallel): independent installers. install_apt_extras is the only apt user.
pids=()
install_terraform  & pids+=("$!")
install_tflint     & pids+=("$!")
install_codex      & pids+=("$!")
ensure_go          & pids+=("$!")
install_apt_extras & pids+=("$!")

fail=0
for pid in "${pids[@]}"; do wait "$pid" || fail=1; done
if [ "$fail" -ne 0 ]; then
  log "ERROR: one or more installs failed (see above); failing so the cache is not poisoned"
  exit 1
fi

log "setup complete: $(terraform version | head -1), $(tflint --version | head -1), go $(go version | awk '{print $3}'), codex $(codex --version 2>/dev/null | head -1), op $(op --version)"

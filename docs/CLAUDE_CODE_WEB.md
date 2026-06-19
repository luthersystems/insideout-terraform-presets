# Claude Code on the web — running this repo in the cloud

This repo is set up to run under [Claude Code on the web](https://code.claude.com/docs/en/claude-code-on-the-web)
(cloud sandboxes at [claude.ai/code](https://claude.ai/code)), so you can dispatch
tickets and get branches/PRs back without running anything on your laptop.

There are two halves: **what lives in this repo** (committed, travels to every cloud
session automatically) and **what you configure once in the web UI** (the environment:
setup script, env vars, network — these cannot be committed).

**Shared environment.** This repo and `reliable` share ONE Claude Code on the web
environment. The setup script below is the **union** of both repos' tools and is committed
identically in both — copy it from either. The environment supplies the tool image + the
1Password token + the network allowlist; each repo's committed `.claude/` config decides
what to run and which secrets to pull. (For a TF-only, leaner environment, drop reliable's
tools from the script.)

## What's in the repo vs the web UI

| Thing | Where | File / location |
|---|---|---|
| System tools (union: terraform, tflint, golangci-lint, gh, op, codex, vercel, git-lfs, go) | repo, **pasted into the shared UI setup script** | [`scripts/cloud-web-setup.sh`](../scripts/cloud-web-setup.sh) |
| Go warmup + **secret fetch from 1Password** + codex auth mode | repo (SessionStart hook) | [`scripts/cloud-session-start.sh`](../scripts/cloud-session-start.sh) |
| Which secrets to pull, as `op://` references (no secret values) | repo | [`scripts/cloud-secrets.op.tpl`](../scripts/cloud-secrets.op.tpl) |
| codex plugin enablement + the hook wiring | repo | `.claude/settings.json` (see below) |
| `OP_SERVICE_ACCOUNT_TOKEN` (scoped 1Password token) | **web UI only** — Environment variables | never committed |
| Network allowlist (`api.openai.com`, `*.1password.com`) | **web UI only** — Network access | never committed |

## Secrets: 1Password service account (not the plaintext env-vars field)

The cloud env-vars field is **plaintext and visible to anyone who can edit the
environment** — the UI warns against putting secrets there, and there is no dedicated
secrets store yet. So instead of dropping real secrets in, we put a **single scoped
1Password service-account token** there and pull the real secrets at session start. This
matches how the rest of the codebase treats 1Password as the source of truth.

**Why this is better:** only one credential sits in the env config, it's **read-only and
scoped to one vault**, it's **centrally rotatable/revocable** in 1Password with an **audit
log**, and the actual secrets (OpenAI key, etc.) never persist in Claude's config or this
repo. Residual risk: the service-account token is still plaintext in the env config, so
keep it least-privilege (read-only, single vault) and rotate it.

### One-time 1Password setup

1. Create a **service account** scoped **read-only** to the `Reliable-Dev` vault
   (1Password → Developer → Service Accounts). Copy its token (`ops_...`).
2. Make sure the secrets you reference exist in that vault. For codex, create an item
   holding your OpenAI key and point [`scripts/cloud-secrets.op.tpl`](../scripts/cloud-secrets.op.tpl)
   at it (`op://Reliable-Dev/<item>/<field>`). The default reference is
   `op://Reliable-Dev/openai-api-key/credential` — adjust to match your item/field.
3. Use a **scoped, spend-capped OpenAI key** so a leak is low-blast-radius.

## One-time web-UI setup

1. **Start a session**: [claude.ai/code](https://claude.ai/code) → **New session** → pick
   `luthersystems/insideout-terraform-presets`.
2. **Open the environment**: click the **cloud icon** (shows the environment name) →
   **Add environment** (or the gear to edit). There is no "Environments" page under
   account Settings — it lives on this selector.
3. **Setup script**: paste the full contents of
   [`scripts/cloud-web-setup.sh`](../scripts/cloud-web-setup.sh). Runs once as root,
   cached ~7 days. Installs Terraform 1.7.5 (matches CI), tflint, gh, Go (if the
   preinstalled one is older than `go.mod`), the OpenAI `codex` CLI, and the `op` CLI.
4. **Environment variables** (`.env` format, one per line, **no quotes**) — just the
   bootstrap token (real secrets come from 1Password at session start):
   ```
   OP_SERVICE_ACCOUNT_TOKEN=ops_...
   ```
   **No GitHub PAT needed up front:** cloud's GitHub proxy authenticates git as your
   connected identity (it's how the private working repo clones), so the private
   `claude-config` marketplace should fetch with no extra token. *Only if* the
   `claude-config:*` skills fail to install, add a read-only fine-grained PAT
   (Contents: read, scoped to `sam-at-luther/claude-config`) as `GH_TOKEN`.
5. **Network access**: switch to **Custom**, keep "include default package managers"
   checked, and add:
   ```
   api.openai.com
   api.firecrawl.dev
   *.1password.com
   ```
   `api.openai.com` is what codex calls at runtime; `api.firecrawl.dev` is for the
   firecrawl plugin; `*.1password.com` covers both the `op` CLI download
   (`downloads.1password.com`) and its runtime API. None are in the default "Trusted" set.
   (If the rare Go-toolchain install path fires, also allow `go.dev`.)

## The `.claude/settings.json` change

This enables the plugins and wires the SessionStart hook for **everyone** who opens this
repo in Claude Code (local and cloud). Add these keys alongside the existing `permissions`
block:

```jsonc
{
  "permissions": { /* unchanged */ },

  "extraKnownMarketplaces": {
    "openai-codex":  { "source": { "source": "github", "repo": "openai/codex-plugin-cc" } },
    "claude-config": { "source": { "source": "github", "repo": "sam-at-luther/claude-config" } }
  },
  "enabledPlugins": {
    "codex@openai-codex": true,
    "claude-config@claude-config": true,
    "firecrawl@claude-plugins-official": true
  },
  "hooks": {
    "SessionStart": [
      {
        "hooks": [
          { "type": "command", "command": "bash \"${CLAUDE_PROJECT_DIR:-.}/scripts/cloud-session-start.sh\"" }
        ]
      }
    ]
  }
}
```

- `claude-config@claude-config` brings qa-professor + pr + golang-guidance etc. It's a
  **private** marketplace; the cloud GitHub proxy should fetch it as your connected
  identity (no token), with the `GH_TOKEN` fallback above if not. **Unverified in cloud**:
  if it doesn't load even with the PAT, fall back to committing those skills into this
  repo's `.claude/agents` + `.claude/skills`.
- `firecrawl@claude-plugins-official` is a bundled Anthropic marketplace, so it needs no
  `extraKnownMarketplaces` entry. Needs `FIRECRAWL_API_KEY` (op-injected) +
  `api.firecrawl.dev` in the allowlist.
- **Use `${CLAUDE_PROJECT_DIR:-.}`, not `$CLAUDE_PROJECT_DIR`, in the hook command.** Local
  Claude Code exports `CLAUDE_PROJECT_DIR`, but the cloud / remote-execution harness (CCR)
  does **not** — with the bare variable the command expands to `bash "/scripts/…"`, a path
  that does not exist, so the SessionStart hook silently no-ops and codex auth + the
  `codex`/`claude-config` plugins (incl. the `/codex:review` skill) never install. The
  `:-.` fallback resolves to the session cwd (the repo root); the script additionally
  re-derives its root from `BASH_SOURCE`.

## How it fits together at session start

```
Cloud env-vars: OP_SERVICE_ACCOUNT_TOKEN  ─┐
                                           ▼
SessionStart hook ── op inject scripts/cloud-secrets.op.tpl
                                           │  (resolves op:// refs)
                                           ▼
                     $CLAUDE_ENV_FILE  (OPENAI_API_KEY=sk-...)
                                           ▼
                     codex plugin reads OPENAI_API_KEY  →  api.openai.com
```

Real secrets exist only inside the isolated VM for the life of the session. Adding a new
secret = add one `op://` line to `scripts/cloud-secrets.op.tpl` (and the item in 1Password).

## Caching: what persists between sessions

Only the **setup-script output** is cached (a filesystem snapshot). Each session is
otherwise a fresh VM, so the Go module cache and Terraform provider downloads **repeat
every session** — the hook pre-warms `go mod download`; Terraform providers
(`aws 6.45.0`, `google 6.10.0`) and tflint plugins download lazily on first
`terraform init` / `tflint --init`.

## Running tickets

- Paste an issue URL or say "work issue #N" — the built-in GitHub tools read issues/PRs
  with no extra setup. Each task runs in its own VM on its own branch, then pushes for review.
- For unattended runs, see [Routines](https://code.claude.com/docs/en/routines) (scheduled
  or API-triggered). GitHub event triggers fire on `pull_request`/`release`, not on `issues`.
- Keep sessions **Private** — a shared/public session link can expose transcript contents.

## Troubleshooting

| Symptom | Fix |
|---|---|
| Hook logs "skipping secret injection" | `OP_SERVICE_ACCOUNT_TOKEN` not set, `op` not installed, or the template is missing. Check the env var and that the setup script ran. |
| `op inject` fails / auth error | Token wrong, expired, or lacks read access to `Reliable-Dev`; or `*.1password.com` not in the Custom allowlist. |
| codex "not authenticated" | The `op://` item/field in `scripts/cloud-secrets.op.tpl` doesn't resolve to a valid key, or `api.openai.com` isn't allowlisted. |
| `go build` fails on a language-version error | Preinstalled Go older than `go.mod`; the setup script installs Go `1.25.0` then (also allowlist `go.dev`). |
| Setup script "cache build failed" | Must finish within ~5 min and exit 0. Check the failing installer in the build log. |

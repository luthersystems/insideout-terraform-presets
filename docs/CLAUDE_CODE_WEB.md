# Claude Code on the web — running this repo in the cloud

This repo is set up to run under [Claude Code on the web](https://code.claude.com/docs/en/claude-code-on-the-web)
(cloud sandboxes at [claude.ai/code](https://claude.ai/code)), so you can dispatch
tickets and get branches/PRs back without running anything on your laptop.

There are two halves: **what lives in this repo** (committed, travels to every cloud
session automatically) and **what you configure once in the web UI** (the environment:
setup script, env vars, network — these cannot be committed).

## What's in the repo vs the web UI

| Thing | Where | File / location |
|---|---|---|
| System tools install (terraform, tflint, gh, go, codex CLI) | repo, **pasted into the UI setup script** | [`scripts/cloud-web-setup.sh`](../scripts/cloud-web-setup.sh) |
| Repo deps + codex auth mode (per session) | repo (SessionStart hook) | [`scripts/cloud-session-start.sh`](../scripts/cloud-session-start.sh) |
| codex plugin enablement + the hook wiring | repo | `.claude/settings.json` (see below) |
| `OPENAI_API_KEY` (codex auth) | **web UI only** — Environment variables | never committed |
| Network allowlist (`api.openai.com`) | **web UI only** — Network access | never committed |

## One-time web-UI setup

1. **Start a session**: [claude.ai/code](https://claude.ai/code) → **New session** → pick
   `luthersystems/insideout-terraform-presets`.
2. **Open the environment**: click the **cloud icon** (shows the current environment name)
   → **Add environment** (or the gear icon to edit). There is no "Environments" page
   under account Settings — it lives on this selector.
3. **Setup script**: paste the full contents of
   [`scripts/cloud-web-setup.sh`](../scripts/cloud-web-setup.sh). Runs once as root,
   cached ~7 days; only re-runs when you change it. Installs Terraform 1.7.5 (matches CI),
   tflint, gh, Go (if the preinstalled one is too old for `go.mod`), and the OpenAI
   `codex` CLI.
4. **Environment variables** (`.env` format, one per line, **no quotes**):
   - `OPENAI_API_KEY=sk-...` — required for the codex plugin. See the security note below.
   - `GITHUB_TOKEN=...` — *optional*, only if `tflint --init` hits GitHub rate limits when
     fetching its aws/google rulesets.
5. **Network access**: switch to **Custom**, keep "include default package managers"
   checked, and add:
   ```
   api.openai.com
   ```
   The defaults already cover `releases.hashicorp.com`, `registry.terraform.io`,
   `github.com`, npm, etc. `api.openai.com` is **not** in the default allowlist, so codex
   is blocked without this.

## The `.claude/settings.json` change

This enables the `codex` plugin and wires the SessionStart hook for **everyone** who opens
this repo in Claude Code (local and cloud). Add these keys alongside the existing
`permissions` block:

```jsonc
{
  "permissions": { /* unchanged */ },

  "extraKnownMarketplaces": {
    "openai-codex": {
      "source": { "source": "github", "repo": "openai/codex-plugin-cc" }
    }
  },
  "enabledPlugins": {
    "codex@openai-codex": true
  },
  "hooks": {
    "SessionStart": [
      {
        "hooks": [
          { "type": "command", "command": "bash \"$CLAUDE_PROJECT_DIR/scripts/cloud-session-start.sh\"" }
        ]
      }
    ]
  }
}
```

## Security: never commit the API key

The OpenAI key goes in the web-UI **Environment variables** field, never in this repo.
GitHub push-protection will block a committed key, OpenAI auto-revokes leaked keys, and it
would be readable by anyone with repo access. The codex CLI reads `OPENAI_API_KEY` from the
environment automatically — nothing about the secret needs to live in git. Note that env
vars are visible to anyone who can edit the environment, so treat that list accordingly.

## Caching: what persists between sessions

Only the **setup-script output** is cached (a filesystem snapshot). Each session is
otherwise a fresh VM, so the Go module cache and Terraform provider downloads **repeat
every session** — the SessionStart hook pre-warms `go mod download`; Terraform providers
(`aws 6.45.0`, `google 6.10.0`) and tflint plugins download lazily the first time a task
runs `terraform init` / `tflint --init`.

## Running tickets

- Paste an issue URL or say "work issue #N" — the built-in GitHub tools read issues/PRs
  with no extra setup. Each task runs in its own VM on its own branch, then pushes for review.
- For unattended runs, see [Routines](https://code.claude.com/docs/en/routines) (scheduled
  or API-triggered). Note GitHub event triggers fire on `pull_request`/`release`, not on
  `issues`.

## Troubleshooting

| Symptom | Fix |
|---|---|
| codex plugin says "not authenticated" | Confirm `OPENAI_API_KEY` is set in the environment's env vars and `api.openai.com` is in the Custom allowlist. |
| codex network errors / timeouts to OpenAI | Network is still on "Trusted" — switch to **Custom** and add `api.openai.com`. |
| `go build` fails on a language-version error | Preinstalled Go is older than `go.mod`; the setup script installs Go `1.25.0` when that happens — re-check it ran. |
| Setup script "cache build failed" | It must finish within ~5 min and exit 0. Check the failing installer in the build log. |
| `tflint --init` rate-limited | Add an optional `GITHUB_TOKEN` env var. |

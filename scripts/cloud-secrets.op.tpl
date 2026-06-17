# Secrets to pull from 1Password into a Claude Code on the web session.
#
# SAFE TO COMMIT: this file contains only `op://` references, never secret values.
# scripts/cloud-session-start.sh resolves these via `op inject` (using the scoped
# OP_SERVICE_ACCOUNT_TOKEN set in the cloud environment) and writes the resolved
# KEY=value lines into the session env. The real secrets never enter the persisted
# environment config or this repo.
#
# Format:  ENV_VAR=op://<vault>/<item>/<field>
# Add a line per secret you need in the sandbox. Vault is `Reliable-Dev` (see the
# project's .env.*.example files for the reference convention).

# Used by the codex CLI/plugin and the firecrawl CLI/plugin. Create these items in
# 1Password (vault Reliable-Dev) and adjust the item/field names to match what you store.
OPENAI_API_KEY=op://Reliable-Dev/openai-api-key/credential
FIRECRAWL_API_KEY=op://Reliable-Dev/firecrawl/api_key

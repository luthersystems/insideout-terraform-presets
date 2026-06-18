# Cloud-only secrets for a Claude Code on the web session, pulled from 1Password.
#
# SAFE TO COMMIT: only 1Password secret references, never secret values.
# scripts/cloud-session-start.sh resolves these with the op CLI (using the scoped
# OP_SERVICE_ACCOUNT_TOKEN) into the session env. Real secret values never enter the
# persisted environment config or this repo.
#
# NOTE: keep this file free of stray "op:" tokens in comments -- op inject does raw
# text substitution and treats any such token as a (malformed) reference.
#
# Used by the codex CLI/plugin and the firecrawl CLI/plugin.
# Items live in 1Password vault Reliable-Dev (openai + firecrawl).
OPENAI_API_KEY=op://Reliable-Dev/openai/api_key
FIRECRAWL_API_KEY=op://Reliable-Dev/firecrawl/credential

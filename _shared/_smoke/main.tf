# Placeholder fixture for issue #203 plumbing — DELETE when a real shared
# module lands in _shared/.
#
# This module exists solely so that:
#   - the embed glob _shared/*/*.tf has at least one matching file (Go's
#     embed requires every glob to match at least one file at compile time);
#   - tests/lint-shared-no-cloud-providers.sh has a real cross-cloud module
#     to scan, satisfying its cardinality floor (>=1 cross-cloud module
#     scanned per run);
#   - tests/validate-as-child.sh has a fixture to confirm top-level `_shared/`
#     is filtered out of preset enumeration.
#
# A no-op module: one local, one output. Holds NO cloud-specific providers
# (no aws, google, google-beta, azurerm) — that constraint is enforced by
# tests/lint-shared-no-cloud-providers.sh and is the defining contract of
# this bucket.

terraform {
  required_version = ">= 1.0"
}

locals {
  smoke = "_shared/_smoke"
}

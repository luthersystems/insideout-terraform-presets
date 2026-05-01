# Placeholder fixture for issue #203 plumbing — DELETE when a real shared
# module lands in gcp/_shared/.
#
# This module exists solely so that:
#   - the embed glob gcp/_shared/*/*.tf has at least one matching file
#     (Go's embed requires every glob to match at least one file at compile
#     time);
#   - tests/validate-as-child.sh has a fixture to exercise the per-cloud
#     `_shared` filter against;
#   - the cross-cloud-no-providers lint has a sibling under per-cloud `_shared`
#     to confirm it does NOT scan GCP-side helpers.
#
# A no-op module: one local, one output. Holds no resources, declares no
# providers.

terraform {
  required_version = ">= 1.0"
}

locals {
  smoke = "gcp/_shared/_smoke"
}

# GCP Cloud KMS module — direct google_kms_* resources.
#
# History: this preset originally wrapped terraform-google-modules/kms/google
# ~> 3.0. That upstream module's `local.keys_by_name` calls slice() on a
# count-controlled splat which can error during plan against an empty state
# with "slice end_index past the length" (issue #180). PR #181 surgically
# wrapped `module.kms.keys` in `try(...)` to unblock the default-config
# customer in #178's repro, but left a hole: when var.iam_bindings was
# non-empty the binding's for_each referenced the same slice expression
# and its plan still failed. This revision (issue #182) replaces the
# upstream module entirely with direct google_kms_* resources keyed by
# for_each, eliminating the slice expression so the failure mode cannot
# recur and the iam_bindings hole is closed by construction.
#
# State migration for existing customers:
#
#   The moved {} blocks at the bottom of this file rebind the upstream's
#   default-config addresses to the new direct-resource addresses. They
#   cover var.keys = [{ name = "default" }] (the default and the only
#   shape used by the in-the-wild repro). Customers with non-default
#   var.keys must run a one-time `terraform state mv` BEFORE applying
#   this revision, e.g. for var.keys = [{name="data"},{name="logs"}]:
#
#     # for prevent_destroy = true (the default):
#     terraform state mv \
#       'module.kms.google_kms_crypto_key.key[0]' \
#       'google_kms_crypto_key.protected["data"]'
#     terraform state mv \
#       'module.kms.google_kms_crypto_key.key[1]' \
#       'google_kms_crypto_key.protected["logs"]'
#     terraform state mv \
#       'module.kms.google_kms_key_ring.key_ring' \
#       'google_kms_key_ring.this'
#
#     # for prevent_destroy = false: swap .key[i]→.key_ephemeral[i] and
#     # protected→ephemeral.
#
#   Indexes correspond to the order of var.keys in the customer's tfvars
#   at the time of last apply. Run `terraform state list` first to
#   confirm. After state-mv, the moved {} blocks below become no-ops on
#   non-existent source addresses (terraform tolerates this — moved blocks
#   describe prior state, not current config).
#
#   If a non-default customer applies without running state-mv, the
#   moved {} blocks rebind the wrong key to "default" and terraform plan
#   attempts to destroy the rest. On prevent_destroy=true keys the plan
#   errors loudly (the lifecycle is the safety net) — no data loss.

# Per-deploy suffix so retries after state loss don't 409 on the undeletable
# keyring shell (issue #159). Stable across applies via state.
resource "random_id" "suffix" {
  byte_length = 4
}

locals {
  keyring_name = "${var.project}-${var.keyring_name}-${random_id.suffix.hex}"

  # for_each-friendly view of var.keys.
  keys_by_name_input = { for k in var.keys : k.name => k }

  # Indexable {name => key_id} map regardless of which prevent_destroy
  # branch populated. Exactly one of the two source maps is non-empty
  # (the for_each on each resource is mutually exclusive on
  # var.prevent_destroy), so merge() is symmetric and produces the same
  # shape the upstream module's `keys` output produced.
  keys_by_name = merge(
    { for n, k in google_kms_crypto_key.protected : n => k.id },
    { for n, k in google_kms_crypto_key.ephemeral : n => k.id },
  )
}

resource "google_kms_key_ring" "this" {
  name     = local.keyring_name
  project  = var.project_id
  location = var.region
}

# Protected branch: lifecycle.prevent_destroy = true (hard-coded literal —
# Terraform requires the value to be static). The for_each population is
# gated on var.prevent_destroy so this resource only exists when the
# caller wants destroy protection.
resource "google_kms_crypto_key" "protected" {
  for_each = var.prevent_destroy ? local.keys_by_name_input : {}

  name            = each.value.name
  key_ring        = google_kms_key_ring.this.id
  rotation_period = each.value.rotation_period
  purpose         = each.value.purpose
  labels          = each.value.labels

  version_template {
    algorithm        = each.value.algorithm
    protection_level = each.value.protection_level
  }

  lifecycle {
    prevent_destroy = true
  }
}

# Ephemeral branch: lifecycle.prevent_destroy = false. Used for dev/test
# stacks and any caller that opts out of destroy protection.
resource "google_kms_crypto_key" "ephemeral" {
  for_each = var.prevent_destroy ? {} : local.keys_by_name_input

  name            = each.value.name
  key_ring        = google_kms_key_ring.this.id
  rotation_period = each.value.rotation_period
  purpose         = each.value.purpose
  labels          = each.value.labels

  version_template {
    algorithm        = each.value.algorithm
    protection_level = each.value.protection_level
  }

  lifecycle {
    prevent_destroy = false
  }
}

# Additional IAM bindings. crypto_key_id consumes local.keys_by_name,
# which is now a plain for_each-derived map (no slice expression). The
# pre-#182 hole — iam_bindings + empty state failing plan because of the
# upstream's slice() — is closed by construction.
resource "google_kms_crypto_key_iam_binding" "this" {
  for_each = { for b in var.iam_bindings : "${b.key_name}-${b.role}" => b }

  crypto_key_id = local.keys_by_name[each.value.key_name]
  role          = each.value.role
  members       = each.value.members
}

# State migration: pre-#182 customers had a single key named "default"
# at module.kms.google_kms_crypto_key.{key|key_ephemeral}[0] under the
# vendored upstream. These blocks rebind those addresses to the new
# for_each-keyed resources so the default-config upgrade is a no-op
# plan. Non-default customers must run the state-mv recipe in this
# file's header comment first.
moved {
  from = module.kms.google_kms_key_ring.key_ring
  to   = google_kms_key_ring.this
}

moved {
  from = module.kms.google_kms_crypto_key.key[0]
  to   = google_kms_crypto_key.protected["default"]
}

moved {
  from = module.kms.google_kms_crypto_key.key_ephemeral[0]
  to   = google_kms_crypto_key.ephemeral["default"]
}

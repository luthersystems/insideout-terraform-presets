# GCP Cloud KMS Module using terraform-google-kms
# https://github.com/terraform-google-modules/terraform-google-kms

# Per-deploy suffix so retries after state loss don't 409 on the undeletable
# keyring shell (issue #159). Stable across applies via state.
resource "random_id" "suffix" {
  byte_length = 4
}

locals {
  keyring_name = "${var.project}-${var.keyring_name}-${random_id.suffix.hex}"

  # The upstream terraform-google-modules/kms/google module's
  # `local.keys_by_name` calls slice() over a count-controlled splat which
  # can error during plan against an empty state with
  # "slice end_index past the length" — the documented failure mode in
  # issue #180 (split out from #178).
  #
  # Per the Terraform docs for try() —
  # https://developer.hashicorp.com/terraform/language/functions/try —
  # try() catches errors raised during evaluation of the wrapped
  # expression, including function-call failures inside transitively-
  # evaluated locals of a referenced module. When we read
  # module.kms.keys, terraform evaluates the upstream's
  # local.keys_by_name, slice() fires, and the error propagates as a
  # diagnostic to our expression context where try() traps it.
  # versions.tf pins required_version >= 1.3 which is the floor for this
  # behavior; we rely on Terraform's documented contract rather than a
  # cross-version-verified test (a real-plan integration test against
  # an empty state is tracked in #182).
  #
  # On the steady-state happy path (default prevent_destroy=true, default
  # var.keys with one entry), slice() is well-formed and try() is a no-op
  # — module.kms.keys returns the real {name => key_id} map.
  #
  # The iam_bindings resource below still indexes into this local
  # directly. When iam_bindings is empty (the default), the local is
  # never evaluated for that resource and the plan proceeds. When it's
  # non-empty AND the upstream errored, the binding's plan fails — that's
  # a known degradation, tracked as #182. The permanent fix is to replace
  # the upstream module with direct google_kms_* resources using for_each
  # (no slice expressions); shipping the surgical fix here unblocks the
  # default-config customer in #178's repro without the migration risk
  # of the upstream replacement.
  keys_by_name = try(module.kms.keys, {})
}

module "kms" {
  source  = "terraform-google-modules/kms/google"
  version = "~> 3.0"

  project_id = var.project_id
  location   = var.region
  keyring    = local.keyring_name

  keys                 = [for k in var.keys : k.name]
  key_rotation_period  = var.keys[0].rotation_period
  key_algorithm        = var.keys[0].algorithm
  key_protection_level = var.keys[0].protection_level
  purpose              = var.keys[0].purpose

  prevent_destroy = var.prevent_destroy

  labels = var.labels
}

# Additional IAM bindings. crypto_key_id consumes local.keys_by_name
# directly; when iam_bindings is empty (the default) the local is not
# evaluated for this resource and the plan proceeds even if the upstream
# slice() errors (issue #180). When iam_bindings is non-empty the failure
# surface is unchanged from before this fix — the surgical fix here
# protects the output path, which is what most consumers depend on.
resource "google_kms_crypto_key_iam_binding" "this" {
  for_each = { for b in var.iam_bindings : "${b.key_name}-${b.role}" => b }

  crypto_key_id = local.keys_by_name[each.value.key_name]
  role          = each.value.role
  members       = each.value.members
}


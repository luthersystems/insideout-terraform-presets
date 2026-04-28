# GCP Cloud KMS Module using terraform-google-kms
# https://github.com/terraform-google-modules/terraform-google-kms

locals {
  keyring_name = "${var.project}-${var.keyring_name}"
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

# Additional IAM bindings
resource "google_kms_crypto_key_iam_binding" "this" {
  for_each = { for b in var.iam_bindings : "${b.key_name}-${b.role}" => b }

  crypto_key_id = module.kms.keys[each.value.key_name]
  role          = each.value.role
  members       = each.value.members
}


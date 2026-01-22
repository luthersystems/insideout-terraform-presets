# GCP Cloud KMS Module using terraform-google-kms
# https://github.com/terraform-google-modules/terraform-google-kms

locals {
  keyring_name = "${var.project}-${var.keyring_name}"
}

module "kms" {
  source  = "terraform-google-modules/kms/google"
  version = "~> 3.0"

  project_id = var.project
  location   = var.region
  keyring    = local.keyring_name

  keys               = [for k in var.keys : k.name]
  key_rotation_period = { for k in var.keys : k.name => k.rotation_period }
  key_algorithm       = { for k in var.keys : k.name => k.algorithm }
  key_protection_level = { for k in var.keys : k.name => k.protection_level }
  purpose             = { for k in var.keys : k.name => k.purpose }

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


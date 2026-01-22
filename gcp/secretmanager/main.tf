# GCP Secret Manager Module
# Using native google_secret_manager_secret resources

locals {
  secrets_map = { for s in var.secrets : s.name => s }
}

# Create secrets
resource "google_secret_manager_secret" "this" {
  for_each = local.secrets_map

  secret_id = "${var.project}-${each.key}"
  project   = var.project

  labels = merge(
    {
      project = var.project
    },
    var.labels,
    each.value.labels
  )

  replication {
    dynamic "auto" {
      for_each = each.value.replication == null || try(each.value.replication.automatic, true) ? [1] : []
      content {}
    }

    dynamic "user_managed" {
      for_each = each.value.replication != null && try(each.value.replication.user_managed, null) != null ? [each.value.replication.user_managed] : []
      content {
        dynamic "replicas" {
          for_each = user_managed.value
          content {
            location = replicas.value.location
            dynamic "customer_managed_encryption" {
              for_each = replicas.value.kms_key_name != null ? [replicas.value.kms_key_name] : []
              content {
                kms_key_name = customer_managed_encryption.value
              }
            }
          }
        }
      }
    }
  }
}

# Create secret versions (if value provided)
resource "google_secret_manager_secret_version" "this" {
  for_each = { for k, v in local.secrets_map : k => v if v.value != null }

  secret      = google_secret_manager_secret.this[each.key].id
  secret_data = each.value.value
}

# IAM bindings
resource "google_secret_manager_secret_iam_binding" "this" {
  for_each = { for b in var.iam_bindings : "${b.secret_name}-${b.role}" => b }

  secret_id = google_secret_manager_secret.this[each.value.secret_name].id
  project   = var.project
  role      = each.value.role
  members   = each.value.members
}


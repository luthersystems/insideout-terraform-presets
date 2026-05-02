# Cloud Run Service
# https://registry.terraform.io/providers/hashicorp/google/latest/docs/resources/cloud_run_v2_service

# Per-deploy suffix so retries after state loss don't 409 on the existing
# Cloud Run service name (issue #159).
resource "random_id" "suffix" {
  byte_length = 4
}

locals {
  service_name = "${var.project}-${var.service_name}-${random_id.suffix.hex}"
}

resource "google_cloud_run_v2_service" "main" {
  name     = local.service_name
  location = var.region
  project  = var.project_id

  template {
    scaling {
      min_instance_count = var.min_instances
      max_instance_count = var.max_instances
    }

    timeout = "${var.timeout_seconds}s"

    service_account = var.service_account_email != "" ? var.service_account_email : null

    dynamic "vpc_access" {
      for_each = var.vpc_connector != "" ? [1] : []
      content {
        connector = var.vpc_connector
        egress    = upper(replace(var.vpc_egress, "-", "_"))
      }
    }

    containers {
      image = var.image

      ports {
        container_port = var.port
      }

      resources {
        limits = {
          cpu    = var.cpu
          memory = var.memory
        }
      }

      dynamic "env" {
        for_each = var.env_vars
        content {
          name  = env.key
          value = env.value
        }
      }
    }

    max_instance_request_concurrency = var.concurrency

    labels      = merge({ project = var.project }, var.labels)
    annotations = var.annotations
  }

  labels      = merge({ project = var.project }, var.labels)
  annotations = var.annotations

  lifecycle {
    ignore_changes = [
      # Ignore changes to the image tag to allow external deployments
      template[0].containers[0].image,
    ]
  }
}

# IAM binding for public access (optional)
resource "google_cloud_run_v2_service_iam_member" "public" {
  count = var.allow_unauthenticated ? 1 : 0

  project  = var.project_id
  location = var.region
  name     = google_cloud_run_v2_service.main.name
  role     = "roles/run.invoker"
  member   = "allUsers"
}

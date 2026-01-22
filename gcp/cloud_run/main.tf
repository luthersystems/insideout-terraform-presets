# Cloud Run Service
# https://registry.terraform.io/providers/hashicorp/google/latest/docs/resources/cloud_run_v2_service

locals {
  service_name = "${var.project}-${var.service_name}"
}

resource "google_cloud_run_v2_service" "main" {
  name     = local.service_name
  location = var.region
  project  = var.project

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

    labels = var.labels
  }

  labels = var.labels

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

  project  = var.project
  location = var.region
  name     = google_cloud_run_v2_service.main.name
  role     = "roles/run.invoker"
  member   = "allUsers"
}

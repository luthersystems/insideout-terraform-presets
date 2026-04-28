# GCP Memorystore (Redis) Instance

terraform {
  required_version = ">= 1.5"
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = ">= 5.0"
    }
    random = {
      source  = "hashicorp/random"
      version = ">= 3.5"
    }
  }
}

# Per-deploy suffix so retries after state loss don't 409 on the existing
# Redis instance name (issue #159).
resource "random_id" "suffix" {
  byte_length = 4
}

resource "google_redis_instance" "this" {
  project            = var.project_id
  name               = "${var.project}-${var.name}-${random_id.suffix.hex}"
  tier               = var.tier
  memory_size_gb     = var.memory_size_gb
  region             = var.region
  authorized_network = var.authorized_network
  redis_version      = var.redis_version

  labels = {
    project = var.project
    managed = "terraform"
  }
}

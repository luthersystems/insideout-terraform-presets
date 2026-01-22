# GCP Memorystore (Redis) Instance

terraform {
  required_version = ">= 1.5"
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = ">= 5.0"
    }
  }
}

resource "google_redis_instance" "this" {
  project            = var.project
  name               = "${var.project}-${var.name}"
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

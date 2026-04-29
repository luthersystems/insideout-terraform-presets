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

# Per-deploy suffix so retries after state loss don't 409 on the trigger
# name (issue #159).
resource "random_id" "suffix" {
  byte_length = 4
}

# Cloud Build and Secret Manager API enables. The pre-#168 default trigger
# referenced a hardcoded Cloud Source Repository named "main-repo" which
# does not exist on a fresh project (CSR is also being deprecated). The
# default is now a webhook trigger backed by a generated secret so the
# module composes cleanly out of the box without any external prereqs.
resource "google_project_service" "cloudbuild" {
  project            = var.project_id
  service            = "cloudbuild.googleapis.com"
  disable_on_destroy = false
}

resource "google_project_service" "secretmanager" {
  project            = var.project_id
  service            = "secretmanager.googleapis.com"
  disable_on_destroy = false
}

resource "random_password" "webhook_secret" {
  length  = 32
  special = false
}

resource "google_secret_manager_secret" "webhook" {
  project   = var.project_id
  secret_id = "${var.project}-cloudbuild-webhook-${random_id.suffix.hex}"

  replication {
    auto {}
  }

  labels = merge({ project = var.project }, var.labels)

  depends_on = [google_project_service.secretmanager]
}

resource "google_secret_manager_secret_version" "webhook" {
  secret      = google_secret_manager_secret.webhook.id
  secret_data = random_password.webhook_secret.result
}

resource "google_cloudbuild_trigger" "trigger" {
  project  = var.project_id
  location = var.region
  name     = "${var.project}-trigger-${random_id.suffix.hex}"

  webhook_config {
    secret = google_secret_manager_secret_version.webhook.id
  }

  build {
    step {
      name = "gcr.io/cloud-builders/gcloud"
      args = ["info"]
    }

    timeout = "60s"
  }

  depends_on = [google_project_service.cloudbuild]
}

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

resource "google_cloudbuild_trigger" "trigger" {
  project  = var.project_id
  name     = "${var.project}-trigger-${random_id.suffix.hex}"
  filename = "cloudbuild.yaml"
  trigger_template {
    branch_name = "main"
    repo_name   = "main-repo"
  }
}

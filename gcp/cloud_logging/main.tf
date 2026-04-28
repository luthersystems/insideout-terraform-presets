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

# Per-deploy suffix so retries after state loss don't 409 on the sink name
# (issue #159).
resource "random_id" "suffix" {
  byte_length = 4
}

resource "google_logging_project_sink" "sink" {
  name        = "${var.project}-sink-${random_id.suffix.hex}"
  destination = "storage.googleapis.com/${var.project}-logs"
  filter      = "severity >= ERROR"
}

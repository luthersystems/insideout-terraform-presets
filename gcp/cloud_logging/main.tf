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
# or bucket name (issue #159).
resource "random_id" "suffix" {
  byte_length = 4
}

# Log archive bucket — sink destination. Created inside this module so the
# preset is batteries-included; the sink had previously referenced
# ${var.project}-logs which no module ever created (issue #166).
resource "google_storage_bucket" "logs" {
  name                        = "${var.project}-logs-${random_id.suffix.hex}"
  project                     = var.project_id
  location                    = var.region
  force_destroy               = true
  uniform_bucket_level_access = true

  lifecycle_rule {
    condition {
      age = var.retention_days
    }
    action {
      type = "Delete"
    }
  }

  labels = merge(
    {
      project = var.project
      managed = "terraform"
    },
    var.labels,
  )
}

resource "google_logging_project_sink" "sink" {
  project     = var.project_id
  name        = "${var.project}-sink-${random_id.suffix.hex}"
  destination = "storage.googleapis.com/${google_storage_bucket.logs.name}"
  filter      = "severity >= ERROR"

  unique_writer_identity = true
}

# Grant the sink's writer identity permission to write to the bucket.
# Without this the sink silently drops every log entry.
resource "google_storage_bucket_iam_member" "sink_writer" {
  bucket = google_storage_bucket.logs.name
  role   = "roles/storage.objectCreator"
  member = google_logging_project_sink.sink.writer_identity
}

terraform {
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

# Per-deploy suffix so retries after state loss don't 409 on the singleton
# database (issue #159). Switching off "(default)" is a deliberate breaking
# change: client SDKs no longer infer the database name — read the
# `database_name` output and pass it explicitly.
resource "random_id" "suffix" {
  byte_length = 4
}

resource "google_firestore_database" "database" {
  project     = var.project_id
  name        = "${var.project}-firestore-${random_id.suffix.hex}"
  location_id = var.location_id != "" ? var.location_id : var.region
  type        = "FIRESTORE_NATIVE"

  # NOTE: etag drifts on refresh but is Computed-only, so
  # lifecycle.ignore_changes has no effect. Suppression must happen at the
  # drift-check level — see sandbox-infrastructure-template#93 (#215).
  # NOTE: earliest_version_time drifts on refresh but is Computed-only, so
  # lifecycle.ignore_changes has no effect. Suppression must happen at the
  # drift-check level — see sandbox-infrastructure-template#93 (#215).
}

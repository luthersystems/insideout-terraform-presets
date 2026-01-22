# GCP Backup Configuration
# Uses Cloud Storage for backups and scheduled snapshots for Compute Engine

terraform {
  required_version = ">= 1.5"
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = ">= 5.0"
    }
  }
}

# GCS bucket for backups
resource "google_storage_bucket" "backups" {
  count    = var.enable_gcs_backups ? 1 : 0
  project  = var.project
  name     = "${var.project}-backups-${var.region}"
  location = var.region

  uniform_bucket_level_access = true
  force_destroy               = false

  versioning {
    enabled = true
  }

  lifecycle_rule {
    action {
      type = "Delete"
    }
    condition {
      age = var.backup_retention_days
    }
  }

  lifecycle_rule {
    action {
      type          = "SetStorageClass"
      storage_class = "COLDLINE"
    }
    condition {
      age = 30
    }
  }

  labels = {
    project = var.project
    purpose = "backups"
    managed = "terraform"
  }
}

# Compute Engine snapshot schedule
resource "google_compute_resource_policy" "snapshot_schedule" {
  count   = var.enable_compute_snapshots ? 1 : 0
  project = var.project
  name    = "${var.project}-snapshot-schedule"
  region  = var.region

  snapshot_schedule_policy {
    schedule {
      daily_schedule {
        days_in_cycle = 1
        start_time    = var.snapshot_start_time
      }
    }

    retention_policy {
      max_retention_days    = var.snapshot_retention_days
      on_source_disk_delete = "KEEP_AUTO_SNAPSHOTS"
    }

    snapshot_properties {
      storage_locations = [var.region]
      labels = {
        project = var.project
        managed = "terraform"
      }
    }
  }
}

# Cloud SQL backups are configured on the Cloud SQL instance itself
# This local captures the backup configuration for reference
locals {
  backup_config = {
    gcs_bucket           = var.enable_gcs_backups ? google_storage_bucket.backups[0].name : null
    snapshot_schedule    = var.enable_compute_snapshots ? google_compute_resource_policy.snapshot_schedule[0].name : null
    retention_days       = var.backup_retention_days
  }
}

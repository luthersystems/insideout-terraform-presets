# GCP Cloud Functions (2nd Gen) Module
# https://registry.terraform.io/providers/hashicorp/google/latest/docs/resources/cloudfunctions2_function

terraform {
  required_version = ">= 1.0"
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

locals {
  function_name = "${var.project}-${var.function_name}"
  bucket_name   = var.source_archive_bucket != "" ? var.source_archive_bucket : "${var.project}-gcf-source-${random_id.bucket_suffix[0].hex}"
}

# Random suffix for auto-created source bucket
resource "random_id" "bucket_suffix" {
  count       = var.source_archive_bucket == "" ? 1 : 0
  byte_length = 4
}

# Source code bucket (created only if not provided)
resource "google_storage_bucket" "source" {
  count = var.source_archive_bucket == "" ? 1 : 0

  name                        = local.bucket_name
  project                     = var.project
  location                    = var.region
  uniform_bucket_level_access = true
  force_destroy               = true

  labels = var.labels
}

# Placeholder source archive object
resource "google_storage_bucket_object" "source" {
  count = var.source_archive_object == "" ? 1 : 0

  name   = "function-source.zip"
  bucket = var.source_archive_bucket != "" ? var.source_archive_bucket : google_storage_bucket.source[0].name
  source = "${path.module}/placeholder.zip"
}

resource "google_cloudfunctions2_function" "this" {
  name     = local.function_name
  location = var.region
  project  = var.project

  build_config {
    runtime     = var.runtime
    entry_point = var.entry_point

    source {
      storage_source {
        bucket = var.source_archive_bucket != "" ? var.source_archive_bucket : google_storage_bucket.source[0].name
        object = var.source_archive_object != "" ? var.source_archive_object : google_storage_bucket_object.source[0].name
      }
    }
  }

  service_config {
    available_memory   = "${var.available_memory_mb}M"
    timeout_seconds    = var.timeout_seconds
    max_instance_count = var.max_instances
    min_instance_count = var.min_instances

    environment_variables = var.env_vars

    vpc_connector                 = var.vpc_connector != "" ? var.vpc_connector : null
    vpc_connector_egress_settings = var.vpc_connector != "" ? var.vpc_egress : null
  }

  labels = var.labels

  lifecycle {
    ignore_changes = [
      # Allow external deployments to update source
      build_config[0].source[0].storage_source[0].object,
    ]
  }
}

# IAM binding for public access (optional)
resource "google_cloudfunctions2_function_iam_member" "public" {
  count = var.allow_unauthenticated ? 1 : 0

  project        = var.project
  location       = var.region
  cloud_function = google_cloudfunctions2_function.this.name
  role           = "roles/cloudfunctions.invoker"
  member         = "allUsers"
}

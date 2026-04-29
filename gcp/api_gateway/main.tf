# GCP API Gateway

terraform {
  required_version = ">= 1.5"
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = ">= 5.0"
    }
    google-beta = {
      source  = "hashicorp/google-beta"
      version = ">= 5.0"
    }
    random = {
      source  = "hashicorp/random"
      version = ">= 3.5"
    }
  }
}

# Per-deploy suffix so retries after state loss don't 409 on the api / gateway
# IDs (issue #159).
resource "random_id" "suffix" {
  byte_length = 4
}

# Rotate api_config_id only when the OpenAPI spec actually changes.
# Previously this used formatdate(timestamp()) which forced replacement
# on every apply — functionally fine (api configs are versioned and
# immutable by design, so the intended deployment model IS create + swap)
# but surfaces as drift_detected=true on every plan even when the spec
# hasn't changed. Reliable's tfstatus flow then shows a false drift
# signal on otherwise-quiescent stacks.
#
# Using random_id with keepers tied to md5(var.openapi_spec):
#   - same spec → same hex → same config_id → no churn → no drift
#   - spec change → new hex → new config created, gateway switches
#     (create_before_destroy on the api_config below makes this safe)
#   - state loss → random_id regenerates → fresh config (the operator
#     cleans up the orphan in GCP — config_ids don't 409 on retry)
resource "random_id" "config_version" {
  byte_length = 4
  keepers = {
    spec_hash = md5(var.openapi_spec)
  }
}

# Enable API Gateway API
resource "google_project_service" "api_gateway" {
  project = var.project_id
  service = "apigateway.googleapis.com"

  disable_on_destroy = false
}

resource "google_project_service" "service_management" {
  project = var.project_id
  service = "servicemanagement.googleapis.com"

  disable_on_destroy = false
}

resource "google_project_service" "service_control" {
  project = var.project_id
  service = "servicecontrol.googleapis.com"

  disable_on_destroy = false
}

# API definition
resource "google_api_gateway_api" "this" {
  provider = google-beta
  project  = var.project_id
  api_id   = "${var.project}-api-${random_id.suffix.hex}"

  labels = {
    project = var.project
    managed = "terraform"
  }

  depends_on = [
    google_project_service.api_gateway,
    google_project_service.service_management,
    google_project_service.service_control,
  ]
}

# API Config (OpenAPI spec)
resource "google_api_gateway_api_config" "this" {
  provider      = google-beta
  project       = var.project_id
  api           = google_api_gateway_api.this.api_id
  api_config_id = "${var.project}-api-config-${random_id.config_version.hex}"

  openapi_documents {
    document {
      path     = "openapi.yaml"
      contents = base64encode(var.openapi_spec)
    }
  }

  gateway_config {
    backend_config {
      google_service_account = var.backend_service_account
    }
  }

  labels = {
    project = var.project
    managed = "terraform"
  }

  lifecycle {
    create_before_destroy = true
  }
}

# Gateway deployment
resource "google_api_gateway_gateway" "this" {
  provider   = google-beta
  project    = var.project_id
  region     = var.region
  api_config = google_api_gateway_api_config.this.id
  gateway_id = "${var.project}-gateway-${random_id.suffix.hex}"

  labels = {
    project = var.project
    managed = "terraform"
  }
}

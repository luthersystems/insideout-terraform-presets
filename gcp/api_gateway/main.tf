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
  }
}

# Enable API Gateway API
resource "google_project_service" "api_gateway" {
  project = var.project
  service = "apigateway.googleapis.com"

  disable_on_destroy = false
}

resource "google_project_service" "service_management" {
  project = var.project
  service = "servicemanagement.googleapis.com"

  disable_on_destroy = false
}

resource "google_project_service" "service_control" {
  project = var.project
  service = "servicecontrol.googleapis.com"

  disable_on_destroy = false
}

# API definition
resource "google_api_gateway_api" "this" {
  provider = google-beta
  project  = var.project
  api_id   = "${var.project}-api"

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
  project       = var.project
  api           = google_api_gateway_api.this.api_id
  api_config_id = "${var.project}-api-config-${formatdate("YYYYMMDDhhmmss", timestamp())}"

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
  project    = var.project
  region     = var.region
  api_config = google_api_gateway_api_config.this.id
  gateway_id = "${var.project}-gateway"

  labels = {
    project = var.project
    managed = "terraform"
  }
}

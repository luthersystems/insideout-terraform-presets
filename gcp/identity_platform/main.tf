# GCP Identity Platform Configuration

terraform {
  required_version = ">= 1.5"
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = ">= 5.0"
    }
  }
}

# Enable Identity Platform API
resource "google_project_service" "identity_platform" {
  project = var.project
  service = "identitytoolkit.googleapis.com"

  disable_on_destroy = false
}

# Identity Platform configuration
resource "google_identity_platform_config" "this" {
  project = var.project

  sign_in {
    allow_duplicate_emails = var.allow_duplicate_emails

    email {
      enabled           = var.enable_email_signin
      password_required = var.password_required
    }

    phone_number {
      enabled = var.enable_phone_signin
    }

    anonymous {
      enabled = var.enable_anonymous_signin
    }
  }

  dynamic "mfa" {
    for_each = var.mfa_enabled ? [1] : []
    content {
      state             = var.mfa_state
      enabled_providers = var.mfa_enabled_providers
    }
  }

  depends_on = [google_project_service.identity_platform]
}

# OAuth client for web applications
resource "google_identity_platform_default_supported_idp_config" "google" {
  count   = var.enable_google_signin ? 1 : 0
  project = var.project

  idp_id        = "google.com"
  client_id     = var.google_client_id
  client_secret = var.google_client_secret

  enabled = true

  depends_on = [google_identity_platform_config.this]
}

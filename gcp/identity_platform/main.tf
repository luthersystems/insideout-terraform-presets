# GCP Identity Platform Configuration
#
# Idempotency contract (issue #197): Identity Platform is a GCP singleton
# product — once enabled on a project, the API has no way to disable it.
# A `terraform destroy` cannot truly remove the underlying state; the
# next `terraform apply` would issue a CREATE that GCP rejects with
# `400 INVALID_PROJECT_ID: Identity Platform has already been enabled
# for this project`. This breaks back-to-back destroy/apply cycles and
# also fails on shared/test projects where IP was previously enabled
# (e.g. customer repro `diagramtest2025-09-14`).
#
# Fix: adopt the existing config via an `import {}` block (TF 1.5+) and
# pin `lifecycle { ignore_changes = all }` so the resource behaves like
# a synthetic data source backed by the provider's own read logic. The
# provider does not ship `data "google_identity_platform_config"`, and
# `data "http"` against `:config` returns 200 in both greenfield and
# previously-enabled cases (so it can't discriminate). The import path
# works for both because the `:config` GET endpoint is always available
# once `identitytoolkit.googleapis.com` is enabled.
#
# Trade-off: module variables (sign_in, mfa, etc.) are advisory under
# this contract — they document intent but are NOT enforced once the
# config is adopted. If GCP later exposes idempotent re-enablement we
# can lift `ignore_changes` and apply variables again.

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
  project = var.project_id
  service = "identitytoolkit.googleapis.com"

  disable_on_destroy = false
}

# Adopt the project's existing Identity Platform config rather than
# attempting CREATE — see header comment (#197). The `:config` GET
# returns 200 with a default body for any project where the API is
# enabled, so this works on both greenfield and previously-enabled
# projects.
import {
  to = google_identity_platform_config.this
  id = "projects/${var.project_id}/config"
}

# Identity Platform configuration. Adopted via `import` above; the
# sign_in/mfa blocks are retained as documented intent but are not
# enforced — see `lifecycle.ignore_changes = all`.
resource "google_identity_platform_config" "this" {
  project = var.project_id

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

  lifecycle {
    # Idempotency contract (#197): config is adopted, not enforced.
    ignore_changes = all
  }

  depends_on = [google_project_service.identity_platform]
}

# OAuth client for web applications
resource "google_identity_platform_default_supported_idp_config" "google" {
  count   = var.enable_google_signin ? 1 : 0
  project = var.project_id

  idp_id        = "google.com"
  client_id     = var.google_client_id
  client_secret = var.google_client_secret

  enabled = true

  depends_on = [google_identity_platform_config.this]

  lifecycle {
    precondition {
      condition     = length(trimspace(var.google_client_id)) > 0 && length(trimspace(var.google_client_secret)) > 0
      error_message = "google_client_id and google_client_secret are required when enable_google_signin is true (issue #168 sibling: prevents Identity Platform's 400 'client_id must not be empty' at apply)."
    }
  }
}

# GCP Identity Platform Configuration
#
# Idempotency contract (issues #197, #199): Identity Platform is a GCP
# singleton — once enabled on a project, the API has no way to disable
# it. `terraform destroy` cannot truly remove the underlying state, so
# the next `terraform apply` would issue a CREATE that GCP rejects with
# `400 INVALID_PROJECT_ID: Identity Platform has already been enabled
# for this project`. This breaks back-to-back destroy/apply cycles and
# also fails on shared/test projects where IP was previously enabled.
#
# v0.7.0 attempted to fix this with a child-module `import {}` block.
# That regressed harder: TF 1.5+ only permits `import {}` blocks in the
# root module, so `terraform init` rejected the bundle outright (#199,
# "An import block was detected in 'module.gcp_identity_platform'.
# Import blocks are only allowed in the root module."). Same constraint
# applies to `removed {}` blocks — both are root-only.
#
# Current state (v0.7.1+): the module CREATEs the resource normally and
# pins `lifecycle { ignore_changes = all }` so that once it lands in
# state, subsequent applies don't fight customer-side tweaks made via
# the GCP console. The first-apply-on-previously-enabled-project failure
# is a known limitation tracked in #199 — the proper fix lives in the
# composer (`luthersystems/reliable`), which must emit a root-level
# `import { to = module.gcp_identity_platform.google_identity_platform_config.this ... }`
# alongside the module instantiation. Until that lands, callers running
# against pre-existing IP projects must `terraform import` the resource
# into state out-of-band before the first apply.
#
# Trade-off: module variables (sign_in, mfa, etc.) ARE applied on
# greenfield projects but are NOT enforced on subsequent applies (per
# `ignore_changes = all`). They document intent and seed the initial
# config; ongoing config changes go through the GCP console or out-of-
# band tooling. This matches the singleton semantics of the underlying
# API.

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

# Identity Platform configuration. CREATEd on greenfield projects;
# `lifecycle.ignore_changes = all` prevents drift fights once in state.
# See header comment for the singleton semantics and the follow-up
# composer change tracked in #199.
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
    # Singleton config: once in state, ignore drift so we don't fight
    # console edits or re-CREATE on a previously-enabled project.
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

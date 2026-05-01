# GCP Identity Platform Configuration
#
# Singleton adoption (issues #197, #199, #201): Identity Platform's
# `projects/<id>/config` resource is a true GCP singleton — once
# enabled, the API has no way to disable it, and CREATE rejects with
# `400 INVALID_PROJECT_ID: Identity Platform has already been enabled
# for this project` on previously-enabled projects.
#
# v0.7.0 attempted a child-module `import {}` block (rejected by TF 1.5+
# root-only rule, #199); v0.7.1 reverted to plain CREATE + lifecycle.
# v0.7.2 lands the canonical fix in-tree: data.google_client_config
# pulls the OAuth2 token the google provider already minted, and
# data.http issues a plan-time REST GET against
# `https://identitytoolkit.googleapis.com/v2/projects/<id>/config`. If
# the GET returns 200, the config already exists and `count = 0` skips
# the CREATE; if 404 (or any other non-2xx — 403 from missing API
# enablement, etc.), `count = 1` proceeds with the CREATE on greenfield.
# No cross-repo coupling, no out-of-band `terraform import` step, no
# gcloud-on-deploy-container assumption.
#
# Trade-off on adopted projects: the module's input variables (sign_in,
# mfa, etc.) are NOT applied to a pre-existing config — `count = 0`
# means the variables seed nothing, and `lifecycle.ignore_changes = all`
# remains as a belt-and-suspenders against any future apply-time drift
# fights on the greenfield path. Ongoing config changes go through the
# GCP console or out-of-band tooling. Matches the singleton semantics
# of the underlying API.
#
# Outputs: `config_name` returns the canonical path
# `projects/<id>/config` regardless of greenfield-vs-adopt. The
# `authorized_domains` output is null on adopted projects since we
# don't have the resource's attribute readout — callers needing this
# on adopted projects must query the IP REST API directly. The
# `adopted` output exposes which path was taken.

terraform {
  required_version = ">= 1.5"
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = ">= 5.0"
    }
    http = {
      source  = "hashicorp/http"
      version = ">= 3.4"
    }
  }
}

# Enable Identity Platform API
resource "google_project_service" "identity_platform" {
  project = var.project_id
  service = "identitytoolkit.googleapis.com"

  disable_on_destroy = false
}

# Singleton existence probe (issue #201). Pull the OAuth2 token the
# google provider already minted via data.google_client_config, then
# issue a single REST GET against the IP config endpoint via
# data.http. The hashicorp/http provider returns status_code without
# erroring on non-2xx responses (a 404 from a missing config is the
# expected greenfield case), so status_code is plan-time-known and
# safe to gate the resource's count on.
#
# No depends_on on google_project_service.identity_platform: that
# would defer status_code to apply time and break the count expression.
# The probe is safe pre-API-enable —
#   greenfield, API disabled: probe returns 403/404 → count=1 → CREATE
#     fires after the API enable lands in apply order
#   previously enabled, API enabled: probe returns 200 → count=0 →
#     SKIP, which is the correct outcome
# The exotic case (API disabled but config already exists from a prior
# session) is outside this module's contract.
data "google_client_config" "current" {}

data "http" "ip_existence_check" {
  url = "https://identitytoolkit.googleapis.com/v2/projects/${var.project_id}/config"
  request_headers = {
    Authorization = "Bearer ${data.google_client_config.current.access_token}"
    Accept        = "application/json"
  }
}

locals {
  # True when CREATE should fire: probe returned anything other than
  # 200 (404 = config doesn't exist yet, 403 = API not enabled, etc.).
  ip_should_create = data.http.ip_existence_check.status_code != 200
}

# Identity Platform configuration. CREATEd on greenfield projects
# (count=1 when the probe returned non-200); SKIPPED on previously-
# enabled projects (count=0 when the probe returned 200).
# `lifecycle.ignore_changes = all` is retained as a defensive pin
# against any future apply-time drift on the greenfield path. See
# header comment for the full adoption rationale (#201).
resource "google_identity_platform_config" "this" {
  count = local.ip_should_create ? 1 : 0

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

# OAuth client for web applications. Gated on both var.enable_google_signin
# and module.adopt.should_create — on adopted projects we don't manage
# the IdP config either, matching the parent's stance of leaving
# pre-existing IP state alone.
resource "google_identity_platform_default_supported_idp_config" "google" {
  count   = var.enable_google_signin && local.ip_should_create ? 1 : 0
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

# -----------------------------------------------------------------------------
# GCP GitHub Actions — Workload Identity Federation (WIF) for keyless deploys
# -----------------------------------------------------------------------------
# Mirrors aws/githubactions's OIDC role UX on the GCP side. Creates a
# Workload Identity Pool + GitHub OIDC provider + deploy service account
# bound such that GitHub Actions workflows in the configured repository can
# impersonate the SA via short-lived federated credentials — eliminating
# the need to mint and rotate long-lived SA JSON keys (the GCP equivalent
# of leaking an AWS access key).
#
# Issue #597 row 1 (GCP GitHub Actions WIF). Follow-up scope (separate
# tickets, filed at PR-merge time):
#   - Cloud Deploy / Cloud Build delivery pipeline preset (gcp/cloud_deploy)
#   - gcp/datadog and gcp/splunk log-router presets
#   - inspector + drift-policy + insideout-import registry entries
#
# Idempotency contract (CLAUDE.md "Idempotency"):
#   - google_iam_workload_identity_pool, ..._provider, google_service_account
#     are NOT singletons — they can be created and destroyed cleanly. No
#     adoption / lifecycle.ignore_changes dance required.
#   - google_project_service entries use disable_on_destroy = false so the
#     APIs stay enabled across destroy/apply cycles (matches the cloud_build
#     and identity_platform conventions in this repo).
#
# IAM propagation: no time_sleep needed. The consumer of these bindings
# is an external GitHub Actions workflow that runs minutes / hours / days
# after terraform apply — IAM has propagated long before then. See the
# trailing comment block in this file for the rule that governs adding
# time_sleep if a future in-stack consumer is wired in.

terraform {
  required_version = ">= 1.0"
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = ">= 5.0"
    }
  }
}

# -----------------------------------------------------------------------------
# Required API enables
# -----------------------------------------------------------------------------
# WIF needs both iam.googleapis.com (for service-account + WIF resource
# CRUD) and iamcredentials.googleapis.com (the token-minting STS endpoint
# the GitHub Action calls to exchange its OIDC token for an SA access
# token). Without the latter, the workflow succeeds at terraform-apply
# time but fails at first deploy with `PERMISSION_DENIED: IAM Service
# Account Credentials API has not been used`.
resource "google_project_service" "iam" {
  project            = var.project_id
  service            = "iam.googleapis.com"
  disable_on_destroy = false
}

resource "google_project_service" "iamcredentials" {
  project            = var.project_id
  service            = "iamcredentials.googleapis.com"
  disable_on_destroy = false
}

resource "google_project_service" "sts" {
  project            = var.project_id
  service            = "sts.googleapis.com"
  disable_on_destroy = false
}

# -----------------------------------------------------------------------------
# Workload Identity Pool — the container that holds the GitHub OIDC provider
# -----------------------------------------------------------------------------
# pool_id has a 4-32 char limit and a `[a-z]([a-z0-9-]*[a-z0-9])` regex.
# `${var.project}-${var.pool_short_name}` keeps the inspector name-prefix
# scoping rule satisfied (lint-labelless-name-prefix.sh) while staying
# within the length cap for typical project prefixes (`io-<13chars>` ≈
# 16 chars + a short suffix). If a caller hits the cap, drop pool_short_name.
resource "google_iam_workload_identity_pool" "github" {
  project                   = var.project_id
  workload_identity_pool_id = "${var.project}-${var.pool_short_name}"
  display_name              = "GitHub Actions (${var.project})"
  description               = "WIF pool for GitHub Actions OIDC federation — repo ${var.github_repository}"

  depends_on = [google_project_service.iam]
}

# -----------------------------------------------------------------------------
# GitHub OIDC provider on the pool
# -----------------------------------------------------------------------------
# `oidc.issuer_uri = https://token.actions.githubusercontent.com` is the
# documented GitHub Actions OIDC issuer. The provider exposes the standard
# GitHub claims (`sub`, `repository`, `actor`, `workflow`, `ref`, ...) which
# we map 1:1 to `attribute.*` so the attribute_condition CEL expression
# below can gate on them.
#
# attribute_condition (load-bearing security control):
#   - Restrict to the configured `var.github_repository` so a workflow in a
#     different repo on the same GitHub OIDC issuer cannot mint credentials.
#   - Restrict to the caller-configured ref patterns (branches, tags,
#     pull_request workflows) so unprivileged contributors can't simply
#     push a branch to gain deploy creds.
# Failing both gates fails the token exchange with `PERMISSION_DENIED:
# The principal ... is not allowed to impersonate ...` — which is the
# correct fail-loud surface.
#
# A missing condition would accept ANY GitHub workflow on the public OIDC
# issuer (i.e. literally the entire world running on github.com). The
# variables.tf validation block rejects the all-empty configuration to
# prevent this misconfiguration from shipping.
resource "google_iam_workload_identity_pool_provider" "github" {
  project                            = var.project_id
  workload_identity_pool_id          = google_iam_workload_identity_pool.github.workload_identity_pool_id
  workload_identity_pool_provider_id = "${var.project}-${var.provider_short_name}"
  display_name                       = "GitHub OIDC (${var.project})"
  description                        = "GitHub Actions OIDC provider for ${var.github_repository}"

  attribute_mapping = {
    "google.subject"             = "assertion.sub"
    "attribute.actor"            = "assertion.actor"
    "attribute.aud"              = "assertion.aud"
    "attribute.repository"       = "assertion.repository"
    "attribute.repository_owner" = "assertion.repository_owner"
    "attribute.ref"              = "assertion.ref"
    "attribute.ref_type"         = "assertion.ref_type"
    "attribute.event_name"       = "assertion.event_name"
  }

  # CEL condition: repository must match AND at least one of the
  # ref-pattern gates must allow the token. local.attribute_condition is
  # composed from var.allowed_branches / var.allowed_tags /
  # var.allowed_pull_request below.
  attribute_condition = local.attribute_condition

  oidc {
    issuer_uri = "https://token.actions.githubusercontent.com"
    # allowed_audiences left empty -> Google accepts the default audience
    # of `https://iam.googleapis.com/projects/<num>/locations/global/
    # workloadIdentityPools/<id>/providers/<provider_id>`, which is what
    # google-github-actions/auth@v2 emits by default. Setting an explicit
    # audience requires the workflow to set `audience:` in the action
    # input — out of scope for the v1 preset UX.
  }

  # Cross-variable validation. Terraform 1.5+ variable validation blocks
  # must reference their own variable, so a multi-variable rule fits
  # cleanly only as a resource precondition. An all-empty ref-pattern
  # configuration would build a WIF provider whose attribute_condition
  # is `attribute.repository == "<repo>" && false` — which rejects every
  # token (the local.ref_group fallback). That's safe-by-default, but
  # the caller's intent is obviously wrong: they configured a deploy
  # provider that can never deploy. Fail loudly at plan instead.
  lifecycle {
    precondition {
      condition = (
        length(var.allowed_branches) > 0 ||
        length(var.allowed_tags) > 0 ||
        var.allowed_pull_request
      )
      error_message = "At least one of allowed_branches, allowed_tags, allowed_pull_request must be non-empty. An all-empty configuration would build a WIF provider that rejects every workflow token (no ref-pattern matches). Pin allowed_branches to [\"main\"] for the standard branch-only deploy workflow, or override the other gates as needed."
    }
  }
}

locals {
  # repository gate: exact match. assertion.repository is "OWNER/REPO".
  repo_clause = "attribute.repository == \"${var.github_repository}\""

  # ref gates: any of the following may match.
  branch_clauses = [
    for b in var.allowed_branches : "attribute.ref == \"refs/heads/${b}\""
  ]
  tag_clauses = [
    for t in var.allowed_tags : "attribute.ref == \"refs/tags/${t}\""
  ]
  pr_clauses = var.allowed_pull_request ? ["attribute.event_name == \"pull_request\""] : []

  ref_clauses = concat(local.branch_clauses, local.tag_clauses, local.pr_clauses)

  # CEL grouping: repo AND (any-ref-clause). When ref_clauses is empty,
  # variables.tf validation already failed plan — but the join would emit
  # an empty parenthetical, so the guard short-circuits to a never-true
  # expression as belt-and-suspenders.
  ref_group = length(local.ref_clauses) > 0 ? "(${join(" || ", local.ref_clauses)})" : "false"

  attribute_condition = "${local.repo_clause} && ${local.ref_group}"
}

# -----------------------------------------------------------------------------
# Deploy service account — the identity the GitHub workflow impersonates
# -----------------------------------------------------------------------------
# account_id has a 4-30 char regex `[a-z]([-a-z0-9]*[a-z0-9])`. The
# var.project prefix can overrun the cap; the short_name default keeps
# typical names under the limit. The display_name carries var.project for
# human readability — the lint-labelless-name-prefix.sh allowlist
# exempts google_service_account from the var.project-in-name rule
# precisely because of this length cap.
resource "google_service_account" "deploy" {
  project      = var.project_id
  account_id   = var.service_account_short_name
  display_name = "GitHub Actions deploy SA (${var.project})"
  description  = "Service account impersonated by GitHub Actions WIF for repo ${var.github_repository}"

  depends_on = [google_project_service.iam]
}

# -----------------------------------------------------------------------------
# Grant the GitHub federated principal permission to impersonate the SA
# -----------------------------------------------------------------------------
# `principalSet://iam.googleapis.com/projects/<NUM>/locations/global/
# workloadIdentityPools/<POOL_ID>/attribute.repository/<OWNER>/<REPO>`
# is the canonical principal-set shape Google publishes for GitHub
# Actions WIF. Combined with the provider's attribute_condition above,
# this allows EXACTLY workflows from `var.github_repository` on the
# allowed refs to mint tokens as this SA.
#
# We resolve the project number via data.google_project rather than
# accepting it as a variable — keeps the caller's contract simple
# (just project_id) and the number is stable per project.
data "google_project" "this" {
  project_id = var.project_id

  # depends_on pins the read until after the API enable lands. Without
  # it data.google_project may race the project_service activation in
  # cold-account deploys and report `number = 0`, which produces an
  # invalid principalSet that 404s at apply.
  depends_on = [google_project_service.iam]
}

resource "google_service_account_iam_binding" "wif_user" {
  service_account_id = google_service_account.deploy.name
  role               = "roles/iam.workloadIdentityUser"

  members = [
    "principalSet://iam.googleapis.com/projects/${data.google_project.this.number}/locations/global/workloadIdentityPools/${google_iam_workload_identity_pool.github.workload_identity_pool_id}/attribute.repository/${var.github_repository}",
  ]

  depends_on = [google_iam_workload_identity_pool_provider.github]
}

# -----------------------------------------------------------------------------
# Project-level role bindings for the deploy SA
# -----------------------------------------------------------------------------
# The SA needs whatever roles the caller's deploy pipeline actually uses.
# Default = roles/run.admin + roles/iam.serviceAccountUser, which is the
# minimal set for a Cloud Run deploy (run.admin to update services,
# serviceAccountUser to attach the runtime SA to the new revision). Callers
# can override via var.deploy_roles for other deploy targets (GKE, Cloud
# Functions, BigQuery jobs, etc.).
#
# google_project_iam_member (additive, per-(role, member) pair) rather
# than google_project_iam_binding (authoritative, owns the whole role
# membership list) to avoid stomping pre-existing role grants in the
# project. Etag drifts on refresh but is Computed-only —
# lifecycle.ignore_changes has no effect; drift-check suppression
# happens at the inspector level (matches gcp/cloud_build:81 pattern).
resource "google_project_iam_member" "deploy_roles" {
  for_each = toset(var.deploy_roles)

  project = var.project_id
  role    = each.value
  member  = "serviceAccount:${google_service_account.deploy.email}"
}

# -----------------------------------------------------------------------------
# IAM propagation note
# -----------------------------------------------------------------------------
# GCP IAM is eventually consistent — a downstream resource that consumes
# bindings can race the propagation and 403 on the first apply. We do NOT
# emit a time_sleep here because the consumer of these bindings is an
# external GitHub Actions workflow that hits the WIF endpoint minutes /
# hours / days after terraform apply completes — IAM will absolutely have
# propagated by then. Compare gcp/cloud_build/main.tf, which historically
# carried a time_sleep but had it removed (#201) once the real cause —
# missing service_account, not propagation — was found. If a future
# in-stack GCP consumer is wired to depend on these bindings at apply
# time, this is the right spot to re-introduce time_sleep.wait_iam_propagation
# (and re-declare the hashicorp/time required_provider above).

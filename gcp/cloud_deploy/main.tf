# -----------------------------------------------------------------------------
# GCP Cloud Deploy — managed delivery-pipeline preset
# -----------------------------------------------------------------------------
# Mirrors the role aws/codepipeline plays for AWS stacks. Provisions a Cloud
# Deploy delivery pipeline that promotes a single release through a serial
# chain of targets (typical: staging -> prod) running on either Cloud Run or
# GKE. Closes the GCP CD-pipeline parity gap (#597 row 2 follow-up, issue
# #613).
#
# Idempotency contract (CLAUDE.md "Idempotency"):
#   - google_clouddeploy_delivery_pipeline, ..._target, google_service_account
#     are NOT singletons; they can be destroyed and recreated cleanly. No
#     adoption / lifecycle.ignore_changes dance required.
#   - google_project_service entries use disable_on_destroy = false so the
#     API stays enabled across destroy/apply cycles (matches the cloud_build
#     and github_actions conventions in this repo).
#
# IAM propagation: the deploy SA's project-level role bindings are consumed
# by Cloud Deploy at release-promotion time (i.e. minutes-to-hours after
# terraform apply when the caller cuts the first release), so no
# time_sleep.wait_iam_propagation is needed at apply time. If a future
# in-stack consumer wires execution at apply time, re-introduce the
# time_sleep pattern from gcp/cloud_build/main.tf.
#
# Var split (#157):
#   - var.project  : naming/label prefix (NOT a GCP project ID)
#   - var.project_id : real GCP project ID (used on every google_* resource
#     and module's `project = ...` argument)

terraform {
  required_version = ">= 1.0"
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = ">= 5.0"
    }
  }
}

locals {
  # Label propagation: every label-capable resource merges var.labels onto
  # { project = var.project } so the project label always lands (mirrors
  # gcp/cloud_build / gcp/github_actions). The downstream InsideOut
  # inspector filters Cloud Deploy resources on the project label for
  # drift attribution.
  tags_or_labels = merge({ project = var.project }, var.labels)

  # Each target's Cloud Deploy identifier is the var.project-prefixed form
  # of the user-supplied short name. The prefix is load-bearing for the
  # inspector's name-prefix scoping (lint-labelless-name-prefix.sh) AND
  # for ensuring two stacks in the same project don't collide on a target
  # named "staging" or "prod". The same prefixed value MUST be used as
  # the serial_pipeline.stages.target_id below — Cloud Deploy resolves
  # stage references by exact name match.
  target_full_names = { for t in var.targets : t.name => "${var.project}-${t.name}" }

  # Re-key the target list by the user-supplied short name for stable
  # for_each. The list-of-objects input var.targets retains author-supplied
  # order (needed for the serial pipeline stage chain below); the map view
  # is only used as the for_each key set on google_clouddeploy_target.
  targets_by_name = { for t in var.targets : t.name => t }
}

# -----------------------------------------------------------------------------
# Required API enables
# -----------------------------------------------------------------------------
# Only clouddeploy.googleapis.com is preset-specific — IAM is already enabled
# by the always-required-services baseline (gcp_services.go). disable_on_destroy
# is false so the API survives a terraform destroy / apply cycle (matches the
# repo-wide convention for project_service activations on long-lived APIs).
resource "google_project_service" "this" {
  for_each = toset(["clouddeploy.googleapis.com"])

  project            = var.project_id
  service            = each.value
  disable_on_destroy = false
}

# -----------------------------------------------------------------------------
# Cloud Deploy execution / runner service account
# -----------------------------------------------------------------------------
# Cloud Deploy executes render/deploy/verify jobs as a service account; the
# default is the Compute Engine default SA, which is over-privileged and
# typically locked-down in production projects. Provision a per-pipeline
# runner SA with least-privilege bindings (releaser + operator below) so
# Cloud Deploy has explicit, auditable identity for its execution.
#
# account_id has a 6-30 char limit and `[a-z]([-a-z0-9]*[a-z0-9])` regex.
# var.project (the stack naming prefix) can overflow when combined with a
# role suffix; the SA short_name default fits within the cap. display_name
# carries var.project for human readability — the
# lint-labelless-name-prefix.sh allowlist exempts google_service_account
# from the var.project-in-name rule precisely because of this length cap.
resource "google_service_account" "deploy_runner" {
  project      = var.project_id
  account_id   = var.service_account_short_name
  display_name = "Cloud Deploy runner SA (${var.project})"
  description  = "Service account Cloud Deploy executes render/deploy/verify jobs as for delivery pipeline ${var.project}-${var.pipeline_short_name}"

  depends_on = [google_project_service.this]
}

# -----------------------------------------------------------------------------
# Delivery pipeline
# -----------------------------------------------------------------------------
# A delivery pipeline pins the ordered serial chain of targets a release
# promotes through. The stage list is built from var.targets in author order
# (so the user controls promotion order). location is var.region — Cloud
# Deploy is a regional service.
#
# The pipeline name is var.project-prefixed so it satisfies the
# lint-labelless-name-prefix rule (labels are also set, but
# google_clouddeploy_delivery_pipeline is not on LABEL_CAPABLE_GCP yet —
# defensive on both axes is cheap insurance).
resource "google_clouddeploy_delivery_pipeline" "this" {
  project  = var.project_id
  location = var.region
  name     = "${var.project}-${var.pipeline_short_name}"

  description = "Delivery pipeline for ${var.project} (managed by Terraform; do not edit in the GCP console)"

  serial_pipeline {
    dynamic "stages" {
      for_each = var.targets
      content {
        # target_id MUST match the corresponding google_clouddeploy_target.name
        # (the var.project-prefixed form built in local.target_full_names) or
        # Cloud Deploy rejects the pipeline at apply with INVALID_ARGUMENT:
        # "target <name> does not exist".
        target_id = local.target_full_names[stages.value.name]
        profiles  = []
      }
    }
  }

  labels = local.tags_or_labels

  depends_on = [
    google_project_service.this,
    google_clouddeploy_target.this,
  ]
}

# -----------------------------------------------------------------------------
# Targets — one per entry in var.targets
# -----------------------------------------------------------------------------
# Each target represents a deployment destination (a Cloud Run region or a
# GKE cluster). The serial_pipeline above chains them in order. The target
# `name` is the pipeline-scoped identifier and must match exactly what the
# stage block references — for_each is keyed by name to make that
# correspondence explicit and to give terraform stable resource addresses
# under target add/remove.
#
# Runtime dispatch:
#   - runtime = "run" : Cloud Run target. runtime_target is the Cloud Run
#     location (region) where the service is deployed.
#   - runtime = "gke" : GKE target. runtime_target is the fully-qualified
#     cluster ID `projects/<id>/locations/<loc>/clusters/<name>`.
#
# Both shapes set execution_configs to drive renders/deploys via the runner
# SA we provisioned above; without an explicit execution_configs block Cloud
# Deploy falls back to the Compute Engine default SA (the over-privileged
# default we explicitly want to avoid).
resource "google_clouddeploy_target" "this" {
  for_each = local.targets_by_name

  project  = var.project_id
  location = var.region
  # name carries var.project as a hard prefix so the InsideOut inspector
  # can attribute the target to this stack via name-prefix scoping
  # (lint-labelless-name-prefix.sh). Using local.target_full_names keeps
  # the prefixing logic and the serial_pipeline stage reference in sync.
  name = local.target_full_names[each.key]

  description = "Cloud Deploy target ${each.value.name} (runtime: ${each.value.runtime}) for pipeline ${var.project}-${var.pipeline_short_name}"

  require_approval = lookup(each.value, "require_approval", false)

  dynamic "run" {
    for_each = each.value.runtime == "run" ? [1] : []
    content {
      location = each.value.runtime_target
    }
  }

  dynamic "gke" {
    for_each = each.value.runtime == "gke" ? [1] : []
    content {
      cluster = each.value.runtime_target
    }
  }

  execution_configs {
    usages            = ["RENDER", "DEPLOY", "VERIFY"]
    service_account   = google_service_account.deploy_runner.email
    execution_timeout = "3600s"
  }

  labels = local.tags_or_labels

  depends_on = [
    google_project_service.this,
    google_project_iam_member.deploy_runner_releaser,
    google_project_iam_member.deploy_runner_operator,
  ]
}

# -----------------------------------------------------------------------------
# Project-level IAM bindings for the runner SA
# -----------------------------------------------------------------------------
# roles/clouddeploy.releaser  — create releases + promote between targets.
# roles/clouddeploy.operator  — execute render/deploy/verify jobs as part
#                               of release rollout.
#
# Both are project-scoped because Cloud Deploy's release / rollout APIs
# operate at the project level. google_project_iam_member (additive) is used
# rather than google_project_iam_binding (authoritative) to avoid stomping
# pre-existing role grants on the project. Etag drifts on refresh but is
# Computed-only — lifecycle.ignore_changes has no effect; drift suppression
# happens at the inspector level (matches gcp/cloud_build:81 pattern).
#
# depends_on pins binding-creation after API enable so first-apply doesn't
# race the Cloud Deploy service-account-readiness check.
resource "google_project_iam_member" "deploy_runner_releaser" {
  project = var.project_id
  role    = "roles/clouddeploy.releaser"
  member  = "serviceAccount:${google_service_account.deploy_runner.email}"

  depends_on = [google_project_service.this]
}

resource "google_project_iam_member" "deploy_runner_operator" {
  project = var.project_id
  role    = "roles/clouddeploy.jobRunner"
  member  = "serviceAccount:${google_service_account.deploy_runner.email}"

  depends_on = [google_project_service.this]
}

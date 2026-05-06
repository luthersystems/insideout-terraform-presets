# Seed resources for the Stage 2d (#264) GCP discover smoke.
#
# One of each of the 5 supported GCP resource types covered by the
# smoke. Labeling follows what gcpdiscover's Cloud Asset filter expects:
# every labelable resource carries `project = <stack-project>` so the
# server-side `labels.project:<stack>` query matches them.
#
# Compute Network has no labels (the resource type pre-dates GCP labels),
# so the discoverer matches it via name-prefix or by being the only
# network in the project. The seed names it ${stack_project}-vpc.
#
# This stack is **never composed by InsideOut** — it's a smoke fixture
# applied directly against the operator's sandbox project by
# tests/gcp-discover-smoke.sh. Operators are expected to run this
# against a throwaway project; the smoke does not destroy on exit so
# subsequent investigation has the resources in place.

variable "project_id" {
  description = "Real GCP project ID (per #157, distinct from var.stack_project). The Cloud Asset Inventory scope is projects/<project_id>."
  type        = string
}

variable "stack_project" {
  description = "InsideOut stack project name. Used as the labels.project value the discoverer filters on, and as the name prefix for resources without label support (Compute Network)."
  type        = string
  default     = "io-smoke-264"
}

variable "region" {
  description = "GCP region for regional resources (the GCS bucket only — every other seeded type is project-global)."
  type        = string
  default     = "us-central1"
}

locals {
  labels = {
    project = var.stack_project
  }
}

# ---------------------------------------------------------------------------
# Pub/Sub topic + subscription. Both project-global; both labelable.
# ---------------------------------------------------------------------------

resource "google_pubsub_topic" "events" {
  project = var.project_id
  name    = "${var.stack_project}-events"
  labels  = local.labels
}

resource "google_pubsub_subscription" "events_sub" {
  project = var.project_id
  name    = "${var.stack_project}-events-sub"
  topic   = google_pubsub_topic.events.id
  labels  = local.labels
}

# ---------------------------------------------------------------------------
# GCS bucket. Regional resource — Identity.Location flows through.
# ---------------------------------------------------------------------------

resource "google_storage_bucket" "data" {
  project                     = var.project_id
  name                        = "${var.stack_project}-data-${var.project_id}"
  location                    = var.region
  uniform_bucket_level_access = true
  force_destroy               = true
  labels                      = local.labels
}

# ---------------------------------------------------------------------------
# Secret Manager secret. Project-global (replication = automatic).
# ---------------------------------------------------------------------------

resource "google_secret_manager_secret" "api_key" {
  project   = var.project_id
  secret_id = "${var.stack_project}-api-key"
  labels    = local.labels

  replication {
    auto {}
  }
}

# ---------------------------------------------------------------------------
# Compute Network. Project-global, label-less — discoverer relies on the
# name-prefix match (mirrors the GCP labelable-vs-name-prefix rule from
# CLAUDE.md / #215).
# ---------------------------------------------------------------------------

resource "google_compute_network" "vpc" {
  project                 = var.project_id
  name                    = "${var.stack_project}-vpc"
  auto_create_subnetworks = false
}

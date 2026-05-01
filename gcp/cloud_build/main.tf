terraform {
  required_version = ">= 1.5"
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = ">= 5.0"
    }
    random = {
      source  = "hashicorp/random"
      version = ">= 3.5"
    }
    time = {
      source  = "hashicorp/time"
      version = ">= 0.9"
    }
  }
}

# Per-deploy suffix so retries after state loss don't 409 on the trigger
# name (issue #159).
resource "random_id" "suffix" {
  byte_length = 4
}

# Cloud Build and Secret Manager API enables. The pre-#168 default trigger
# referenced a hardcoded Cloud Source Repository named "main-repo" which
# does not exist on a fresh project (CSR is also being deprecated). The
# default is now a webhook trigger backed by a generated secret so the
# module composes cleanly out of the box without any external prereqs.
resource "google_project_service" "cloudbuild" {
  project            = var.project_id
  service            = "cloudbuild.googleapis.com"
  disable_on_destroy = false
}

resource "google_project_service" "secretmanager" {
  project            = var.project_id
  service            = "secretmanager.googleapis.com"
  disable_on_destroy = false
}

resource "random_password" "webhook_secret" {
  length  = 32
  special = false
}

resource "google_secret_manager_secret" "webhook" {
  project   = var.project_id
  secret_id = "${var.project}-cloudbuild-webhook-${random_id.suffix.hex}"

  replication {
    auto {}
  }

  labels = merge({ project = var.project }, var.labels)

  depends_on = [google_project_service.secretmanager]
}

resource "google_secret_manager_secret_version" "webhook" {
  secret      = google_secret_manager_secret.webhook.id
  secret_data = random_password.webhook_secret.result
}

# The Cloud Build P4SA (service-PROJECT_NUMBER@gcp-sa-cloudbuild) needs
# roles/secretmanager.secretAccessor on the webhook secret BEFORE the
# trigger is created — otherwise google_cloudbuild_trigger validates
# secret access on the create call and rejects with
# `400 INVALID_ARGUMENT: Request contains an invalid argument` (issue
# #190). Without this binding the trigger create fails on every
# fresh-project deploy.
#
# We resolve the project number from data.google_project rather than
# adopting the google-beta provider just for `google_project_service_identity`.
# The P4SA is created by GCP automatically when cloudbuild.googleapis.com
# is enabled (`google_project_service.cloudbuild` above), and the
# data source's depends_on pins read-after-enable so the project
# number is always available. The SA email shape
# `service-${project_number}@gcp-sa-cloudbuild.iam.gserviceaccount.com`
# is a stable Google contract for Cloud Build P4SAs.
data "google_project" "this" {
  project_id = var.project_id

  depends_on = [google_project_service.cloudbuild]
}

locals {
  cloudbuild_service_agent = "serviceAccount:service-${data.google_project.this.number}@gcp-sa-cloudbuild.iam.gserviceaccount.com"
}

resource "google_secret_manager_secret_iam_member" "cloudbuild_webhook_accessor" {
  project   = var.project_id
  secret_id = google_secret_manager_secret.webhook.secret_id
  role      = "roles/secretmanager.secretAccessor"
  # local.cloudbuild_service_agent reads data.google_project.this.number,
  # which the depends_on above defers to the apply phase — so `member`
  # shows as `(known after apply)` in the plan. The binding still
  # creates correctly; only the plan readout is delayed.
  member = local.cloudbuild_service_agent
}

# IAM propagation wait (issue #197). PR #191 added the secretAccessor
# binding above and a `depends_on` from the trigger to it, but the
# trigger create still hit `400 INVALID_ARGUMENT` on the customer's
# repro. Root cause: GCP IAM is eventually consistent. `depends_on`
# orders the create call but not the propagation of the binding to
# the Cloud Build trigger validator. Resource-level Secret Manager
# bindings have observed propagation of ~60s p50 / ~120s p99, so 90s
# covers the long tail. `time_sleep` only fires on creation — no
# penalty on subsequent applies (and no penalty on destroy/apply
# cycles where this resource is re-created from scratch each time).
resource "time_sleep" "wait_iam_propagation" {
  depends_on      = [google_secret_manager_secret_iam_member.cloudbuild_webhook_accessor]
  create_duration = "90s"
}

resource "google_cloudbuild_trigger" "trigger" {
  project  = var.project_id
  location = var.region
  name     = "${var.project}-trigger-${random_id.suffix.hex}"

  webhook_config {
    secret = google_secret_manager_secret_version.webhook.id
  }

  build {
    step {
      name = "gcr.io/cloud-builders/gcloud"
      args = ["info"]
    }

    timeout = "60s"
  }

  # Wait for IAM propagation before issuing the trigger create — see
  # `time_sleep.wait_iam_propagation` comment above (#197). The
  # `time_sleep` itself depends on the IAM binding, so this single
  # depends_on transitively orders binding → propagation → create.
  depends_on = [
    google_project_service.cloudbuild,
    time_sleep.wait_iam_propagation,
  ]
}

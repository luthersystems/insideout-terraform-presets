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

# BYOSA runner SA (issue #201). Cloud Build's regional webhook trigger
# API rejects CREATE with `400 INVALID_ARGUMENT` (no fieldViolations[])
# when `service_account` is omitted. Reproducing outside Terraform with
# `gcloud builds triggers create webhook` confirmed the missing
# `--service-account` flag is the sole cause; the IAM-propagation
# hypothesis from #190/#197 was incorrect.
#
# account_id has a 4-30 char limit and a `[a-z]([-a-z0-9]*[a-z0-9])`
# regex. var.project (the stack naming prefix, not the GCP project ID)
# can be up to ~50 chars and would overflow when combined with the
# random suffix; the runner SA is project-local so the random suffix
# alone is enough disambiguation. The display_name carries the project
# label for human readability.
resource "google_service_account" "cloudbuild_runner" {
  project      = var.project_id
  account_id   = "cb-runner-${random_id.suffix.hex}" # 18 chars, fits the 30-char limit
  display_name = "Cloud Build webhook trigger runner (project ${var.project})"
}

resource "google_project_iam_member" "cloudbuild_runner_builds_builder" {
  project = var.project_id
  role    = "roles/cloudbuild.builds.builder"
  member  = "serviceAccount:${google_service_account.cloudbuild_runner.email}"
}

# The Cloud Build P4SA (service-PROJECT_NUMBER@gcp-sa-cloudbuild) needs
# roles/secretmanager.secretAccessor on the webhook secret so the trigger
# can read the webhook secret at runtime. Without this binding the
# trigger creates but webhook calls fail at invocation time.
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

resource "google_cloudbuild_trigger" "trigger" {
  project  = var.project_id
  location = var.region
  name     = "${var.project}-trigger-${random_id.suffix.hex}"

  # BYOSA: Cloud Build's regional webhook trigger API rejects CREATE
  # with `400 INVALID_ARGUMENT` (no fieldViolations[]) when
  # service_account is omitted. See header comment on
  # google_service_account.cloudbuild_runner above (#201).
  service_account = google_service_account.cloudbuild_runner.id

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

  depends_on = [
    google_project_service.cloudbuild,
    google_secret_manager_secret_iam_member.cloudbuild_webhook_accessor,
    google_project_iam_member.cloudbuild_runner_builds_builder,
  ]
}

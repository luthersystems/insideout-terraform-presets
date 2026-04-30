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
  member    = local.cloudbuild_service_agent
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

  # The IAM binding must exist before trigger create — see #190 comment
  # block above the data source. Without this depends_on the binding
  # races the trigger create call.
  depends_on = [
    google_project_service.cloudbuild,
    google_secret_manager_secret_iam_member.cloudbuild_webhook_accessor,
  ]
}

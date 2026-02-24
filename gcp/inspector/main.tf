# Inspector Service Account
# Creates a GCP service account that the InsideOut inspector pipeline
# uses to perform read-only inspection of GCP resources.

# -----------------------------------------------------------------------------
# Inspector Service Account
# -----------------------------------------------------------------------------
resource "google_service_account" "inspector" {
  project      = var.project
  account_id   = "insideout-inspector-${var.short_project_id}"
  display_name = "InsideOut Inspector (${var.short_project_id})"
}

# -----------------------------------------------------------------------------
# Viewer roles for the inspector SA at project level
# -----------------------------------------------------------------------------
resource "google_project_iam_member" "inspector" {
  for_each = toset(var.inspector_roles)

  project = var.project
  role    = each.value
  member  = "serviceAccount:${google_service_account.inspector.email}"
}

# -----------------------------------------------------------------------------
# Allow the deployment SA to generate access tokens for the inspector SA
# -----------------------------------------------------------------------------
resource "google_service_account_iam_member" "token_creator" {
  service_account_id = google_service_account.inspector.name
  role               = "roles/iam.serviceAccountTokenCreator"
  member             = "serviceAccount:${var.deployment_sa_email}"
}

resource "google_cloudbuild_trigger" "trigger" {
  name = "main-trigger"
  filename = "cloudbuild.yaml"
  trigger_template {
    branch_name = "main"
    repo_name   = "main-repo"
  }
}

mock_provider "google" {}

# Regression for #190. google_cloudbuild_trigger validates the webhook
# secret is accessible to the Cloud Build P4SA on the create call;
# without roles/secretmanager.secretAccessor the create fails with
# `400 INVALID_ARGUMENT: Request contains an invalid argument`. These
# cases pin that the IAM binding is declared and that the trigger
# depends on it (so the binding propagates before the trigger create
# request fires).

run "defaults_plan" {
  command = plan

  variables {
    project    = "test"
    project_id = "test-project"
  }
}

run "issue_190_iam_binding_resource_present" {
  command = plan

  variables {
    project    = "test"
    project_id = "test-project"
  }

  # The role is a literal in main.tf so it's known at plan time. The
  # member field is computed from data.google_project.this.number,
  # which mock_provider leaves unknown at plan time — the SA shape is
  # pinned by the structural test in pkg/composer/compose_vm_test.go
  # (TestGetPresetFiles_GCP_CloudBuild_HasWebhookSecretIAM) instead.
  assert {
    condition     = google_secret_manager_secret_iam_member.cloudbuild_webhook_accessor.role == "roles/secretmanager.secretAccessor"
    error_message = "the cloud_build webhook IAM binding must grant roles/secretmanager.secretAccessor (issue #190)"
  }
}

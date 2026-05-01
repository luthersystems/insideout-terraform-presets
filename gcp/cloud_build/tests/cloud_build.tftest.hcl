mock_provider "google" {}

# Smoke for the cloud_build preset under mock_provider. Pins that the
# default config plans cleanly — the mock provider can't reproduce
# the issue #190 / #201 INVALID_ARGUMENT failures (those are real
# GCP-side responses on trigger create). The meaningful issue-#190
# and issue-#201 pins live in the structural Go test
# (pkg/composer/compose_vm_test.go::TestGetPresetFiles_GCP_CloudBuild_HasWebhookSecretIAM
# and pkg/composer/cloud_build_byosa_test.go::TestCloudBuildTriggerHasServiceAccount)
# which check IAM binding shape and the BYOSA service_account argument
# on every google_cloudbuild_trigger in the preset library — all of
# which survive any code change that would re-open the regressions.

run "defaults_plan" {
  command = plan

  variables {
    project    = "test"
    project_id = "test-project"
  }
}

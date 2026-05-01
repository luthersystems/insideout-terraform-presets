mock_provider "google" {}

# Smoke for the cloud_build preset under mock_provider. Pins that the
# default config plans cleanly — the mock provider can't reproduce
# the issue #190 INVALID_ARGUMENT failure (that's a real GCP-side
# response on trigger create), and asserting on the IAM binding
# resource's literal-string fields would be tautological. The
# meaningful issue #190 pins live in the structural Go test
# (pkg/composer/compose_vm_test.go::TestGetPresetFiles_GCP_CloudBuild_HasWebhookSecretIAM)
# which checks the data source, IAM binding resource, role string,
# P4SA email shape, and the trigger's depends_on — all of which
# survive any code change that would re-open #190.

run "defaults_plan" {
  command = plan

  variables {
    project    = "test"
    project_id = "test-project"
  }
}

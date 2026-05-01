# Smoke for the identity_platform preset under mock_provider. Pins that
# the default config plans cleanly with the new `import {}` block in
# place — the mock provider can't reproduce the issue #197 INVALID_PROJECT_ID
# failure (that's a real GCP-side response on CREATE against an
# already-enabled project). The structural pin for the import block +
# `lifecycle { ignore_changes = all }` lives in the Go test
# (pkg/composer/compose_vm_test.go::TestGetPresetFiles_GCP_IdentityPlatform_HasIdempotentImport)
# which any code change reverting #197 would break.
#
# Mock providers cannot natively process `import {}` blocks, so we
# `override_resource` to seed the imported resource's state. The shape
# of the override is what we'd want the import to produce — minimal
# values matching the resource schema. Real-cloud verification of the
# import path itself happens in the manual `tfdeploy` repro on issue
# #197.
mock_provider "google" {
  override_resource {
    target = google_identity_platform_config.this
    values = {
      name               = "projects/test-project/config"
      authorized_domains = []
    }
  }
}

run "defaults_plan" {
  command = plan

  variables {
    project    = "test"
    project_id = "test-project"
  }
}

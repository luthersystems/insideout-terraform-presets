# Smoke for the identity_platform preset under mock_provider. Pins that
# the default config plans cleanly on a greenfield project (CREATE path).
# The singleton "already enabled" failure is a real GCP-side response on
# CREATE against a previously-enabled project and cannot be reproduced
# under mock_provider — that path is exercised by the manual real-cloud
# repro on issues #197 / #199.
#
# v0.7.0 attempted child-module `import {}` adoption; v0.7.1 reverts
# that since TF 1.5+ allows `import {}` blocks only in the root module
# (see #199). The structural pin that this module contains NO top-level
# `import {}` block lives in the Go test
# (pkg/composer/compose_vm_test.go::TestGetPresetFiles_GCP_IdentityPlatform_NoRootOnlyBlocks).
mock_provider "google" {}

run "defaults_plan" {
  command = plan

  variables {
    project    = "test"
    project_id = "test-project"
  }
}

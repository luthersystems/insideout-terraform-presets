# Smoke for the identity_platform preset under mock_provider, exercising
# both branches of the v0.7.2 adoption logic (issue #201): greenfield
# (helper returns 404 → count=1, CREATE) and adopted (helper returns
# 200 → count=0, SKIP). override_data pins the helper's data.http
# status_code so the test does not need real GCP credentials.
#
# v0.7.0 attempted child-module `import {}` adoption; v0.7.1 reverted
# that since TF 1.5+ allows `import {}` blocks only in the root module
# (#199). The structural pin that this module contains NO top-level
# `import {}` block lives in the Go test
# (pkg/composer/compose_vm_test.go::TestGetPresetFiles_GCP_IdentityPlatform_NoRootOnlyBlocks).

mock_provider "google" {}
mock_provider "http" {}

variables {
  project    = "test"
  project_id = "test-project"
}

run "greenfield_creates_config" {
  command = plan

  override_data {
    target = data.http.ip_existence_check
    values = {
      status_code = 404
    }
  }

  assert {
    condition     = length(google_identity_platform_config.this) == 1
    error_message = "On greenfield (404) the IP config must be CREATEd (count == 1)"
  }
  assert {
    condition     = output.adopted == false
    error_message = "adopted output must be false when the existence probe returned 404"
  }
}

run "previously_enabled_skips_create" {
  command = plan

  override_data {
    target = data.http.ip_existence_check
    values = {
      status_code = 200
    }
  }

  assert {
    condition     = length(google_identity_platform_config.this) == 0
    error_message = "On previously-enabled (200) the IP config must be SKIPPED (count == 0)"
  }
  assert {
    condition     = output.adopted == true
    error_message = "adopted output must be true when the existence probe returned 200"
  }
  assert {
    condition     = output.config_name == "projects/test-project/config"
    error_message = "config_name must always return the canonical path regardless of greenfield-vs-adopt"
  }
}

# IdP config is also gated on module.adopt.should_create — on adopted
# projects we don't manage Google sign-in either, matching the parent's
# stance of leaving pre-existing IP state alone.
run "google_signin_skipped_when_adopted" {
  command = plan

  variables {
    project              = "test"
    project_id           = "test-project"
    enable_google_signin = true
    google_client_id     = "test-client-id"
    google_client_secret = "test-client-secret"
  }

  override_data {
    target = data.http.ip_existence_check
    values = {
      status_code = 200
    }
  }

  assert {
    condition     = length(google_identity_platform_default_supported_idp_config.google) == 0
    error_message = "Google sign-in IdP config must be SKIPPED on adopted projects (count == 0)"
  }
}

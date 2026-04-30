mock_provider "google" {}

# Regression for #141. The wrapper passes one shared rotation_period /
# algorithm / protection_level / purpose into each google_kms_crypto_key
# instance and var.keys validates that all keys agree on those four
# fields — these tests pin both halves. The homogeneity validations are
# retained for public-API stability across the issue #182 upstream
# replacement; they no longer reflect a technical constraint of the
# implementation (the per-key for_each could legally take heterogeneous
# values) but expanding the surface is out of scope for #182.

run "defaults_plan" {
  command = plan

  variables {
    project    = "test"
    project_id = "test-project"
  }
}

run "multiple_homogeneous_keys" {
  command = plan

  variables {
    project    = "test"
    project_id = "test-project"
    keys = [
      { name = "data" },
      { name = "logs" },
    ]
  }
}

run "rejects_heterogeneous_rotation_period" {
  command = plan

  variables {
    project    = "test"
    project_id = "test-project"
    keys = [
      { name = "a", rotation_period = "7776000s" },
      { name = "b", rotation_period = "2592000s" },
    ]
  }

  expect_failures = [var.keys]
}

run "rejects_heterogeneous_algorithm" {
  command = plan

  variables {
    project    = "test"
    project_id = "test-project"
    keys = [
      { name = "a", algorithm = "GOOGLE_SYMMETRIC_ENCRYPTION" },
      { name = "b", algorithm = "RSA_SIGN_PSS_2048_SHA256" },
    ]
  }

  expect_failures = [var.keys]
}

run "rejects_heterogeneous_protection_level" {
  command = plan

  variables {
    project    = "test"
    project_id = "test-project"
    keys = [
      { name = "a", protection_level = "SOFTWARE" },
      { name = "b", protection_level = "HSM" },
    ]
  }

  expect_failures = [var.keys]
}

run "rejects_heterogeneous_purpose" {
  command = plan

  variables {
    project    = "test"
    project_id = "test-project"
    keys = [
      { name = "a", purpose = "ENCRYPT_DECRYPT" },
      { name = "b", purpose = "ASYMMETRIC_SIGN" },
    ]
  }

  expect_failures = [var.keys]
}

run "rejects_empty_keys" {
  command = plan

  variables {
    project    = "test"
    project_id = "test-project"
    keys       = []
  }

  expect_failures = [var.keys]
}

# Issue #157: var.project_id has a regex validation enforcing GCP's project
# ID rules. A typo that loosens the regex (e.g. {4,28} -> {4,128}) would
# silently let invalid values through. These two cases pin the validation:
# uppercase and underscore are explicit invalid shapes that the regex
# must reject.

run "rejects_uppercase_project_id" {
  command = plan

  variables {
    project    = "test"
    project_id = "BadProject"
  }

  expect_failures = [var.project_id]
}

run "rejects_underscore_project_id" {
  command = plan

  variables {
    project    = "test"
    project_id = "bad_project_id"
  }

  expect_failures = [var.project_id]
}

# Regression for #180/#182. Issue #180 was the upstream
# terraform-google-modules/kms/google's `local.keys_by_name` calling
# slice() on a count-controlled splat which can error during plan
# against an empty state. PR #181 surgically wrapped that consumption
# in `try(...)`. Issue #182 replaced the upstream entirely with direct
# google_kms_* resources keyed by for_each, so the slice expression is
# gone. These cases continue to pin the empty-state plan path:
#
#   - With prevent_destroy=true (default), google_kms_crypto_key.protected
#     fires for_each. plan must succeed.
#   - With prevent_destroy=false, google_kms_crypto_key.ephemeral fires
#     instead. plan must succeed.
#   - With non-empty iam_bindings, google_kms_crypto_key_iam_binding.this
#     resolves crypto_key_id from local.keys_by_name (a plain for_each
#     map, no slice). This is the case PR #181 could not protect.
#
# Both branches must work; if a future change reintroduces a slice
# expression here or re-vendors the upstream, real customers see
# plan_summary.stage_errors=["custom-stack-provision"].

run "issue_180_prevent_destroy_true_plans_clean" {
  command = plan

  variables {
    project         = "test"
    project_id      = "test-project"
    prevent_destroy = true
  }
}

run "issue_180_prevent_destroy_false_plans_clean" {
  command = plan

  variables {
    project         = "test"
    project_id      = "test-project"
    prevent_destroy = false
  }
}

run "issue_180_iam_bindings_resolve_against_local" {
  command = plan

  variables {
    project    = "test"
    project_id = "test-project"
    keys       = [{ name = "data" }]
    iam_bindings = [
      {
        key_name = "data"
        role     = "roles/cloudkms.cryptoKeyEncrypter"
        members  = ["serviceAccount:test@test-project.iam.gserviceaccount.com"]
      }
    ]
  }
}

# Issue #182: the prevent_destroy split is two distinct resources
# (google_kms_crypto_key.protected vs .ephemeral) gated by mutually
# exclusive for_each. These cases pin which branch fires for each
# var.prevent_destroy value, so a refactor that collapses both into one
# resource — or flips the gating direction — fails CI.

run "issue_182_prevent_destroy_true_uses_protected_branch" {
  command = plan

  variables {
    project         = "test"
    project_id      = "test-project"
    prevent_destroy = true
    keys            = [{ name = "data" }]
  }

  assert {
    condition     = length(google_kms_crypto_key.protected) == 1
    error_message = "prevent_destroy=true must populate google_kms_crypto_key.protected"
  }
  assert {
    condition     = length(google_kms_crypto_key.ephemeral) == 0
    error_message = "prevent_destroy=true must leave google_kms_crypto_key.ephemeral empty"
  }
}

run "issue_182_prevent_destroy_false_uses_ephemeral_branch" {
  command = plan

  variables {
    project         = "test"
    project_id      = "test-project"
    prevent_destroy = false
    keys            = [{ name = "data" }]
  }

  assert {
    condition     = length(google_kms_crypto_key.ephemeral) == 1
    error_message = "prevent_destroy=false must populate google_kms_crypto_key.ephemeral"
  }
  assert {
    condition     = length(google_kms_crypto_key.protected) == 0
    error_message = "prevent_destroy=false must leave google_kms_crypto_key.protected empty"
  }
}

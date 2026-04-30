mock_provider "google" {}

# Regression for #141. terraform-google-modules/kms ~> 3.0 declares
# key_rotation_period / key_algorithm / key_protection_level / purpose as
# scalar string. A previous revision of main.tf passed map(string) values
# built from `for k in var.keys : ...`, which the upstream module rejected
# with `string required` at apply time. The wrapper now passes scalars
# from var.keys[0] and the variable validates that all keys agree on
# those four fields — these tests pin both halves.

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

# Regression for #180. The upstream terraform-google-modules/kms/google's
# `local.keys_by_name` calls slice() over a count-controlled splat which
# can error on first plan against an empty state. Our preset wraps that
# value in `try(module.kms.keys, {})` so plan progresses even if the
# upstream errors during evaluation. These two cases pin the bypass
# semantics:
#
#   - With prevent_destroy=true (default), the upstream's slice fires on
#     google_kms_crypto_key.key (count=N). plan must succeed.
#   - With prevent_destroy=false, the upstream picks the ephemeral
#     branch's slice. plan must succeed.
#
# Both arms must work; if either regresses to a slice-end-index error,
# real customers see plan_summary.stage_errors=["custom-stack-provision"].

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

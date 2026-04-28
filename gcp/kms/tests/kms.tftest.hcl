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

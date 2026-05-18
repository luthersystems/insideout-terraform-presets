mock_provider "google" {}

# Issue #613 (gcp/cloud_deploy delivery-pipeline preset) shape tests. Verifies
# that:
#   - Defaults compose cleanly (happy path, two-stage Cloud Run pipeline).
#   - The target name regex / runtime enum / empty-list validations all reject
#     the misconfigurations they're supposed to.
#   - Caller-supplied target list flows through and gets var.project-prefixed
#     before reaching google_clouddeploy_target.name (which the
#     lint-labelless-name-prefix rule enforces).
#   - Duplicate target names fail loudly (they would otherwise silently
#     collapse pipeline stages).

run "cloud_deploy_minimum_inputs" {
  command = plan

  variables {
    project    = "test"
    project_id = "test-project"
  }

  assert {
    condition     = google_clouddeploy_delivery_pipeline.this.name == "test-delivery"
    error_message = "pipeline name should default to ${var.project}-${var.pipeline_short_name}."
  }

  # Default var.targets has two entries (staging, prod) -> two
  # google_clouddeploy_target resources.
  assert {
    condition     = length(google_clouddeploy_target.this) == 2
    error_message = "Default var.targets has two entries; expected two google_clouddeploy_target resources."
  }

  # Each target name carries var.project as a hard prefix (lint contract).
  assert {
    condition     = google_clouddeploy_target.this["staging"].name == "test-staging"
    error_message = "Target name must be var.project-prefixed (lint-labelless-name-prefix contract)."
  }

  assert {
    condition     = google_clouddeploy_target.this["prod"].name == "test-prod"
    error_message = "Target name must be var.project-prefixed (lint-labelless-name-prefix contract)."
  }
}

run "cloud_deploy_gke_target_dispatches_gke_block" {
  command = plan

  variables {
    project    = "test"
    project_id = "test-project"
    targets = [
      {
        name             = "prod"
        runtime          = "gke"
        runtime_target   = "projects/p/locations/us-central1/clusters/c"
        require_approval = true
      },
    ]
  }

  # The dynamic gke {} block fires for runtime="gke" (and run {} stays empty).
  assert {
    condition     = length(google_clouddeploy_target.this["prod"].gke) == 1
    error_message = "runtime=\"gke\" must dispatch the dynamic gke {} block."
  }

  assert {
    condition     = length(google_clouddeploy_target.this["prod"].run) == 0
    error_message = "runtime=\"gke\" must NOT emit a run {} block."
  }
}

# -----------------------------------------------------------------------------
# Negative cases: validation blocks must reject obvious misconfigurations at
# plan time so callers don't discover them at apply.
# -----------------------------------------------------------------------------

run "cloud_deploy_rejects_empty_targets" {
  command = plan

  variables {
    project    = "test"
    project_id = "test-project"
    targets    = []
  }

  expect_failures = [var.targets]
}

run "cloud_deploy_rejects_invalid_runtime" {
  command = plan

  variables {
    project    = "test"
    project_id = "test-project"
    targets = [
      {
        name           = "staging"
        runtime        = "lambda" # not in the allowed enum
        runtime_target = "us-central1"
      },
    ]
  }

  expect_failures = [var.targets]
}

run "cloud_deploy_rejects_duplicate_target_names" {
  command = plan

  variables {
    project    = "test"
    project_id = "test-project"
    targets = [
      { name = "prod", runtime = "run", runtime_target = "us-central1" },
      { name = "prod", runtime = "run", runtime_target = "us-east1" }, # duplicate
    ]
  }

  expect_failures = [var.targets]
}

run "cloud_deploy_rejects_invalid_target_name" {
  command = plan

  variables {
    project    = "test"
    project_id = "test-project"
    targets = [
      {
        name           = "Prod" # uppercase rejected by Cloud Deploy regex
        runtime        = "run"
        runtime_target = "us-central1"
      },
    ]
  }

  expect_failures = [var.targets]
}

run "cloud_deploy_rejects_invalid_project_id" {
  command = plan

  variables {
    project    = "test"
    project_id = "INVALID_UPPERCASE"
  }

  expect_failures = [var.project_id]
}

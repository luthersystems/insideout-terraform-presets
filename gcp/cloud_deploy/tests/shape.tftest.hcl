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
  # `command = apply` (not plan) so the assertions below can cross-reference
  # google_service_account.deploy_runner.email — the email is Computed by
  # the Google provider and is plan-time-unknown. The mock_provider above
  # generates a deterministic mock value at apply, making the cross-ref
  # evaluable. All assertions in this block are about static configuration
  # (names, labels, structural counts), not real API behaviour, so the
  # mock-provider apply is safe.
  command = apply

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

  # serial_pipeline.stages[*].target_id must equal the var.project-prefixed
  # target name (= local.target_full_names entry). Cloud Deploy resolves
  # stages to targets by exact name match — a drift here would compose a
  # plan that apply-time rejects with INVALID_ARGUMENT "target X does not
  # exist". Pinning the correspondence in a tftest keeps the
  # local.target_full_names indirection from accidentally diverging from
  # the stage target_id reference.
  assert {
    condition     = tolist(google_clouddeploy_delivery_pipeline.this.serial_pipeline[0].stages)[0].target_id == "test-staging"
    error_message = "serial_pipeline.stages[0].target_id must be the var.project-prefixed first target name."
  }

  assert {
    condition     = tolist(google_clouddeploy_delivery_pipeline.this.serial_pipeline[0].stages)[1].target_id == "test-prod"
    error_message = "serial_pipeline.stages[1].target_id must be the var.project-prefixed second target name."
  }

  # Runner SA's account_id must hit the variables.tf default. A rename
  # that slipped under the SA's 30-char cap would not trip the validation
  # block but WOULD change a stack-stable identifier consumers might
  # already grant cross-project access to — surface the default
  # explicitly so a default-flip is a deliberate test break.
  assert {
    condition     = google_service_account.deploy_runner.account_id == "clouddeploy-runner"
    error_message = "Runner SA account_id should default to `clouddeploy-runner` (length-safe at 18 chars)."
  }

  # execution_configs.service_account MUST point at the per-pipeline
  # runner SA, not the default Compute Engine SA. The entire reason this
  # preset provisions a runner SA is to avoid the over-privileged CE
  # default — a mutation that dropped execution_configs (or pointed it
  # at the wrong reference) would silently regress that security promise.
  assert {
    condition     = google_clouddeploy_target.this["staging"].execution_configs[0].service_account == google_service_account.deploy_runner.email
    error_message = "Cloud Deploy targets must execute as the per-pipeline runner SA, not the default Compute Engine SA (over-privileged)."
  }

  assert {
    condition     = contains(google_clouddeploy_target.this["staging"].execution_configs[0].usages, "DEPLOY")
    error_message = "execution_configs.usages must include DEPLOY so render/deploy/verify all route through the runner SA."
  }
}

# Positive companion to the cloud_deploy_rejects_* triad below. With a
# valid single-entry targets list (same SHAPE the negatives use, just
# with valid values), plan must succeed. This triangulates that the
# negatives' expect_failures on var.targets is the right validation
# firing (the field under test), not e.g. the empty-list check
# unexpectedly tripping on a single-entry list, or the name regex
# tripping on the targets we use in the duplicate-names case.
run "cloud_deploy_single_valid_target_plans_cleanly" {
  command = plan

  variables {
    project    = "test"
    project_id = "test-project"
    targets = [
      { name = "prod", runtime = "run", runtime_target = "us-central1" },
    ]
  }

  assert {
    condition     = length(google_clouddeploy_target.this) == 1
    error_message = "Valid single-target list must plan cleanly with exactly one target resource."
  }

  assert {
    condition     = google_clouddeploy_target.this["prod"].name == "test-prod"
    error_message = "Single target must still get the var.project name-prefix."
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

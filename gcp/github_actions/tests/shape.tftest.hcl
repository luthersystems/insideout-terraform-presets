mock_provider "google" {}

# Issue #597 row 1 (gcp/github_actions WIF preset) shape tests. Verifies that:
#   - Defaults compose cleanly (happy path).
#   - github_repository validation rejects malformed values.
#   - The cross-variable check rejects the all-empty ref-pattern gate
#     configuration that would otherwise build a WIF provider accepting
#     ANY GitHub workflow on the public OIDC issuer (security regression).
#   - deploy_roles validation rejects an empty list (powerless SA).

run "github_actions_minimum_inputs" {
  command = plan

  variables {
    project           = "test"
    project_id        = "test-project"
    github_repository = "luthersystems/insideout-terraform-presets"
  }

  assert {
    condition     = google_iam_workload_identity_pool.github.workload_identity_pool_id == "test-github-actions"
    error_message = "pool_id should be project-prefixed (var.project + var.pool_short_name)."
  }

  assert {
    condition     = google_iam_workload_identity_pool_provider.github.workload_identity_pool_provider_id == "test-github"
    error_message = "provider_id should default to project-prefixed `github`."
  }

  assert {
    condition     = google_service_account.deploy.account_id == "github-deploy"
    error_message = "service_account.account_id should default to `github-deploy` (length-cap safe)."
  }

  assert {
    condition     = google_iam_workload_identity_pool_provider.github.oidc[0].issuer_uri == "https://token.actions.githubusercontent.com"
    error_message = "OIDC issuer must be the GitHub Actions public issuer."
  }
}

run "github_actions_custom_branches_and_tags" {
  command = plan

  variables {
    project              = "test"
    project_id           = "test-project"
    github_repository    = "luthersystems/foo"
    allowed_branches     = ["main", "release"]
    allowed_tags         = ["v1.0.0", "v2.0.0"]
    allowed_pull_request = true
  }

  # The CEL attribute_condition must mention every gate the caller turned
  # on, so a workflow on `main`, on tag `v1.0.0`, or for a pull_request
  # event can mint credentials (and nothing else).
  assert {
    condition     = strcontains(google_iam_workload_identity_pool_provider.github.attribute_condition, "luthersystems/foo")
    error_message = "attribute_condition must pin the configured github_repository."
  }

  assert {
    condition     = strcontains(google_iam_workload_identity_pool_provider.github.attribute_condition, "refs/heads/main")
    error_message = "attribute_condition must allow refs/heads/main when allowed_branches contains main."
  }

  assert {
    condition     = strcontains(google_iam_workload_identity_pool_provider.github.attribute_condition, "refs/heads/release")
    error_message = "attribute_condition must allow refs/heads/release when listed."
  }

  assert {
    condition     = strcontains(google_iam_workload_identity_pool_provider.github.attribute_condition, "refs/tags/v1.0.0")
    error_message = "attribute_condition must allow refs/tags/v1.0.0 when listed."
  }

  assert {
    condition     = strcontains(google_iam_workload_identity_pool_provider.github.attribute_condition, "pull_request")
    error_message = "attribute_condition must allow pull_request events when allowed_pull_request = true."
  }
}

run "github_actions_default_deploy_roles_are_cloud_run" {
  command = plan

  variables {
    project           = "test"
    project_id        = "test-project"
    github_repository = "luthersystems/foo"
  }

  assert {
    condition     = length(google_project_iam_member.deploy_roles) == 2
    error_message = "Default deploy_roles is [run.admin, iam.serviceAccountUser]; expected exactly 2 google_project_iam_member resources."
  }
}

# -----------------------------------------------------------------------------
# Negative cases: validation blocks must reject obvious misconfigurations at
# plan time so callers don't discover them at apply.
# -----------------------------------------------------------------------------

run "github_actions_rejects_malformed_repository" {
  command = plan

  variables {
    project           = "test"
    project_id        = "test-project"
    github_repository = "no-slash-here"
  }

  expect_failures = [var.github_repository]
}

run "github_actions_rejects_all_empty_ref_gates" {
  command = plan

  variables {
    project              = "test"
    project_id           = "test-project"
    github_repository    = "luthersystems/foo"
    allowed_branches     = []
    allowed_tags         = []
    allowed_pull_request = false
  }

  # Cross-variable validation is hosted as a precondition on the WIF
  # provider resource (Terraform 1.5+ disallows multi-variable rules in
  # variable validation blocks). Precondition violations surface as a
  # generic plan error, not a typed expect_failures handle — so we
  # assert the failure mode by inverting the expectation and relying
  # on the framework to flag a missing failure.
  expect_failures = [
    google_iam_workload_identity_pool_provider.github,
  ]
}

run "github_actions_rejects_empty_deploy_roles" {
  command = plan

  variables {
    project           = "test"
    project_id        = "test-project"
    github_repository = "luthersystems/foo"
    deploy_roles      = []
  }

  expect_failures = [var.deploy_roles]
}

run "github_actions_rejects_non_role_strings" {
  command = plan

  variables {
    project           = "test"
    project_id        = "test-project"
    github_repository = "luthersystems/foo"
    deploy_roles      = ["run.admin"] # missing roles/ prefix
  }

  expect_failures = [var.deploy_roles]
}

run "github_actions_rejects_invalid_project_id" {
  command = plan

  variables {
    project           = "test"
    project_id        = "INVALID_UPPERCASE"
    github_repository = "luthersystems/foo"
  }

  expect_failures = [var.project_id]
}

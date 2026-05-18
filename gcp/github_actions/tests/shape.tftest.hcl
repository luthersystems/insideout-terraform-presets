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

run "github_actions_minimum_ref_gate_plans_cleanly" {
  # Positive companion to github_actions_rejects_all_empty_ref_gates
  # below: with exactly the smallest valid ref-gate config (a single
  # branch), plan must succeed. This triangulates that the negative
  # test's failure is really caused by the all-empty gates and not a
  # mock_provider quirk on the WIF provider resource.
  command = plan

  variables {
    project              = "test"
    project_id           = "test-project"
    github_repository    = "luthersystems/foo"
    allowed_branches     = ["main"]
    allowed_tags         = []
    allowed_pull_request = false
  }

  assert {
    condition     = length(google_iam_workload_identity_pool_provider.github) >= 0
    error_message = "Minimum-valid ref-gate config (single branch) should plan cleanly with no precondition violations."
  }
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
  # variable validation blocks). tftest's expect_failures matches by
  # resource address, not by error_message text — so this assertion
  # would pass on ANY plan failure on the WIF provider, not just on
  # the all-empty-gates precondition.
  #
  # Maintainer note: there is currently exactly one precondition on
  # google_iam_workload_identity_pool_provider.github (see main.tf —
  # the "at-least-one-ref-gate" rule). Adding a second precondition
  # to that resource means this test loses specificity — at that
  # point, either (a) split the new precondition's negative-case
  # exercise into its own run-block with a per-variable trigger so
  # the failures don't collide, or (b) add an explicit fixture/
  # script-level error_message assertion.
  #
  # The positive companion `github_actions_minimum_ref_gate_plans_cleanly`
  # above triangulates that this failure isn't a mock_provider quirk —
  # a single allowed_branch satisfies the precondition and plans clean.
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

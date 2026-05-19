mock_provider "aws" {}
mock_provider "random" {}

# Issue #615 (aws/sagemaker Studio preset) shape tests. Verifies:
#   - Defaults compose cleanly (minimum inputs happy path).
#   - Domain name carries the var.project prefix (inspector attribution).
#   - Project tag is on every taggable resource (defense-in-depth alongside
#     the prefix, CLAUDE.md issue #81).
#   - Studio execution role's trust policy is correctly scoped:
#     - allows sagemaker.amazonaws.com
#     - carries the aws:SourceAccount confused-deputy guard
#     - attaches the default AmazonSageMakerFullAccess managed policy
#   - Workspace bucket toggles between preset-managed (versioning +
#     encryption + public-access-block ALL on) and caller-supplied.
#   - VPC mode flips app_network_access_type correctly.
#   - Studio user profiles attach to the right domain.
#   - Validation rejects every misconfiguration axis, AND a positive
#     companion run pins that the negatives fire on the right rule
#     (rejection-axis triangulation per #614).
#
# Every run uses `command = plan`. The assertions below reference
# attributes the preset sets explicitly (names, tags, IAM trust-policy
# JSON, policy ARN) — all plan-time-known. Apply mode would let us
# also evaluate Computed cross-refs (e.g. studio.id), but the AWS
# provider validates ARN format at apply against the mocked random
# strings mock_provider emits for arn-shaped Computed fields, which
# fails the SageMaker domain's execution_role check. The trade-off is
# acceptable here because every wiring cross-ref we care about is also
# pinned end-to-end in `TestComposeStack_AWSSageMaker_Forward` in the
# Go composer wiring test.

# -----------------------------------------------------------------------------
# Positive: minimum inputs apply cleanly.
# -----------------------------------------------------------------------------

run "sagemaker_minimum_inputs" {
  command = plan

  variables {
    project    = "test"
    vpc_id     = "vpc-12345"
    subnet_ids = ["subnet-aaa"]
  }

  assert {
    condition     = aws_sagemaker_domain.studio.domain_name == "test-studio"
    error_message = "domain_name should be project-prefixed (`<project>-studio`) so the InsideOut inspector's name-prefix scoping works."
  }

  assert {
    condition     = aws_sagemaker_domain.studio.auth_mode == "IAM"
    error_message = "auth_mode must be IAM (the preset doesn't support SSO mode yet)."
  }

  assert {
    condition     = aws_sagemaker_domain.studio.app_network_access_type == "PublicInternetOnly"
    error_message = "app_network_access_type should default to PublicInternetOnly when network_mode is unset (AWS-managed egress)."
  }

  assert {
    condition     = aws_sagemaker_domain.studio.tags["Project"] == "test"
    error_message = "Project tag must be set on the SageMaker domain so the InsideOut inspector's exact-match filter sees it (CLAUDE.md issue #81)."
  }

  assert {
    condition     = aws_iam_role.studio_execution.name == "test-sagemaker-execution"
    error_message = "Execution role should be project-prefixed."
  }

  # Trust-policy correctness. A mutation that swapped the service
  # principal to e.g. lambda.amazonaws.com or removed the
  # aws:SourceAccount Condition block would survive every other
  # assertion in this run, so pin both substrings explicitly.
  assert {
    condition     = strcontains(aws_iam_role.studio_execution.assume_role_policy, "sagemaker.amazonaws.com")
    error_message = "Execution role assume_role_policy must trust sagemaker.amazonaws.com."
  }

  assert {
    condition     = strcontains(aws_iam_role.studio_execution.assume_role_policy, "aws:SourceAccount")
    error_message = "Execution role assume_role_policy must carry the aws:SourceAccount confused-deputy guard."
  }

  assert {
    condition     = aws_iam_role_policy_attachment.studio_managed.policy_arn == "arn:aws:iam::aws:policy/AmazonSageMakerFullAccess"
    error_message = "Default managed-policy attachment must be AmazonSageMakerFullAccess."
  }
}

# -----------------------------------------------------------------------------
# Positive: workspace bucket is preset-created with full hardening.
# -----------------------------------------------------------------------------

run "sagemaker_creates_workspace_bucket_by_default" {
  command = plan

  variables {
    project    = "test"
    vpc_id     = "vpc-12345"
    subnet_ids = ["subnet-aaa"]
  }

  assert {
    condition     = length(aws_s3_bucket.workspace) == 1
    error_message = "Preset must create a workspace S3 bucket when workspace_bucket is null."
  }

  assert {
    condition     = startswith(aws_s3_bucket.workspace[0].bucket, "test-sagemaker-workspace-")
    error_message = "Preset-created bucket name must be project-prefixed (`<project>-sagemaker-workspace-<random>`)."
  }

  assert {
    condition     = length(aws_s3_bucket_public_access_block.workspace) == 1
    error_message = "Public-access-block resource must exist on the preset-created workspace bucket."
  }

  # Encryption + versioning positive assertions — a refactor that
  # accidentally deleted either sibling resource would otherwise pass
  # this run (the caller-supplied companion below pins length == 0,
  # but that's the negative form).
  assert {
    condition     = length(aws_s3_bucket_versioning.workspace) == 1
    error_message = "Versioning resource must exist on the preset-created workspace bucket."
  }

  assert {
    condition     = length(aws_s3_bucket_server_side_encryption_configuration.workspace) == 1
    error_message = "SSE configuration resource must exist on the preset-created workspace bucket."
  }
}

# -----------------------------------------------------------------------------
# Positive: caller-supplied workspace_bucket suppresses every preset S3 resource.
# -----------------------------------------------------------------------------

run "sagemaker_adopts_caller_supplied_workspace_bucket" {
  command = plan

  variables {
    project          = "test"
    vpc_id           = "vpc-12345"
    subnet_ids       = ["subnet-aaa"]
    workspace_bucket = "my-existing-bucket"
  }

  assert {
    condition     = length(aws_s3_bucket.workspace) == 0
    error_message = "Preset must NOT create a workspace S3 bucket when workspace_bucket is supplied."
  }

  assert {
    condition     = length(aws_s3_bucket_versioning.workspace) == 0
    error_message = "Preset must NOT manage versioning on a caller-supplied bucket (caller owns the bucket lifecycle)."
  }

  assert {
    condition     = length(aws_s3_bucket_server_side_encryption_configuration.workspace) == 0
    error_message = "Preset must NOT manage encryption on a caller-supplied bucket (caller owns the bucket lifecycle)."
  }
}

# -----------------------------------------------------------------------------
# Positive: VpcOnly mode flips app_network_access_type and propagates inputs.
# -----------------------------------------------------------------------------

run "sagemaker_vpc_only_mode_flips_network_access" {
  command = plan

  variables {
    project      = "test"
    vpc_id       = "vpc-12345"
    subnet_ids   = ["subnet-aaa", "subnet-bbb"]
    network_mode = "VpcOnly"
  }

  assert {
    condition     = aws_sagemaker_domain.studio.app_network_access_type == "VpcOnly"
    error_message = "app_network_access_type must flip to VpcOnly when network_mode = VpcOnly."
  }

  assert {
    condition     = aws_sagemaker_domain.studio.vpc_id == "vpc-12345"
    error_message = "Domain vpc_id must propagate var.vpc_id."
  }

  assert {
    condition     = length(aws_sagemaker_domain.studio.subnet_ids) == 2
    error_message = "Domain subnet_ids must propagate var.subnet_ids."
  }
}

# -----------------------------------------------------------------------------
# Positive: Studio user profiles attach to the preset's domain.
# -----------------------------------------------------------------------------

run "sagemaker_studio_users_create_profiles" {
  command = plan

  variables {
    project      = "test"
    vpc_id       = "vpc-12345"
    subnet_ids   = ["subnet-aaa"]
    studio_users = ["alice", "bob"]
  }

  assert {
    condition     = length(aws_sagemaker_user_profile.studio_user) == 2
    error_message = "Expected one user-profile per studio_users entry."
  }

  assert {
    condition     = aws_sagemaker_user_profile.studio_user["alice"].user_profile_name == "alice"
    error_message = "Per-user profile name must match the studio_users entry."
  }

  # We can't pin `domain_id == aws_sagemaker_domain.studio.id` under
  # plan mode (both sides are Computed → unknown → assertion silently
  # skipped). The Go composer wiring test
  # `TestComposeStack_AWSSageMaker_Forward` exercises this wiring path
  # end-to-end against the rendered composed root.
}

run "sagemaker_no_studio_users_no_profiles" {
  command = plan

  variables {
    project    = "test"
    vpc_id     = "vpc-12345"
    subnet_ids = ["subnet-aaa"]
  }

  assert {
    condition     = length(aws_sagemaker_user_profile.studio_user) == 0
    error_message = "Empty studio_users list must produce zero user-profile resources."
  }
}

# -----------------------------------------------------------------------------
# Positive triangulation companions for the rejection-axis negatives below.
# Pattern from #614 (`cloud_deploy_single_valid_target_plans_cleanly`):
# without these, a negative could pass for the wrong reason (e.g. the
# `alltrue` short-circuiting before the regex even fires).
# -----------------------------------------------------------------------------

run "sagemaker_valid_user_name_plans_cleanly" {
  command = plan

  variables {
    project      = "test"
    vpc_id       = "vpc-12345"
    subnet_ids   = ["subnet-aaa"]
    studio_users = ["valid-user"]
  }

  assert {
    condition     = length(aws_sagemaker_user_profile.studio_user) == 1
    error_message = "A regex-valid studio_users entry must plan cleanly (companion to rejects_bad_user_name)."
  }
}

run "sagemaker_valid_workspace_bucket_plans_cleanly" {
  command = plan

  variables {
    project          = "test"
    vpc_id           = "vpc-12345"
    subnet_ids       = ["subnet-aaa"]
    workspace_bucket = "valid-bucket-name"
  }

  assert {
    condition     = length(aws_s3_bucket.workspace) == 0
    error_message = "A regex-valid workspace_bucket must plan cleanly with the preset's S3 resources suppressed (companion to rejects_bad_workspace_bucket_name)."
  }
}

run "sagemaker_valid_policy_arn_plans_cleanly" {
  command = plan

  variables {
    project                      = "test"
    vpc_id                       = "vpc-12345"
    subnet_ids                   = ["subnet-aaa"]
    sagemaker_managed_policy_arn = "arn:aws:iam::123456789012:policy/MyScopedSageMaker"
  }

  assert {
    condition     = aws_iam_role_policy_attachment.studio_managed.policy_arn == "arn:aws:iam::123456789012:policy/MyScopedSageMaker"
    error_message = "A regex-valid sagemaker_managed_policy_arn must plan cleanly and propagate to the attachment (companion to rejects_bad_policy_arn)."
  }
}

# -----------------------------------------------------------------------------
# Negative cases — validation must reject obvious misconfigurations at plan
# time so callers don't discover them at apply. Each axis is paired with a
# positive companion above for rejection-axis triangulation.
# -----------------------------------------------------------------------------

run "sagemaker_rejects_bad_user_name" {
  command = plan

  variables {
    project      = "test"
    vpc_id       = "vpc-12345"
    subnet_ids   = ["subnet-aaa"]
    studio_users = ["has spaces"]
  }

  expect_failures = [var.studio_users]
}

run "sagemaker_rejects_bad_workspace_bucket_name" {
  command = plan

  variables {
    project          = "test"
    vpc_id           = "vpc-12345"
    subnet_ids       = ["subnet-aaa"]
    workspace_bucket = "Invalid_Bucket"
  }

  expect_failures = [var.workspace_bucket]
}

run "sagemaker_rejects_bad_policy_arn" {
  command = plan

  variables {
    project                      = "test"
    vpc_id                       = "vpc-12345"
    subnet_ids                   = ["subnet-aaa"]
    sagemaker_managed_policy_arn = "not-an-arn"
  }

  expect_failures = [var.sagemaker_managed_policy_arn]
}

run "sagemaker_rejects_bad_network_mode" {
  command = plan

  variables {
    project      = "test"
    vpc_id       = "vpc-12345"
    subnet_ids   = ["subnet-aaa"]
    network_mode = "InvalidMode"
  }

  expect_failures = [var.network_mode]
}

run "sagemaker_rejects_empty_project" {
  command = plan

  variables {
    project    = "   "
    vpc_id     = "vpc-12345"
    subnet_ids = ["subnet-aaa"]
  }

  expect_failures = [var.project]
}

# Pins the new 35-char project cap added to keep the preset-managed
# S3 bucket name inside AWS's 63-char limit.
run "sagemaker_rejects_oversized_project" {
  command = plan

  variables {
    # 36 chars — one over the limit. The trimspace-non-empty validation
    # passes; only the length validation should fire.
    project    = "abcdefghijklmnopqrstuvwxyz1234567890"
    vpc_id     = "vpc-12345"
    subnet_ids = ["subnet-aaa"]
  }

  expect_failures = [var.project]
}

run "sagemaker_rejects_empty_subnet_ids" {
  command = plan

  variables {
    project    = "test"
    vpc_id     = "vpc-12345"
    subnet_ids = []
  }

  expect_failures = [var.subnet_ids]
}

run "sagemaker_rejects_empty_vpc_id" {
  command = plan

  variables {
    project    = "test"
    vpc_id     = "   "
    subnet_ids = ["subnet-aaa"]
  }

  expect_failures = [var.vpc_id]
}

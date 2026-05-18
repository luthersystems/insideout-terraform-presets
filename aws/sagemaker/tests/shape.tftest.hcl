mock_provider "aws" {}

# Issue #615 (aws/sagemaker Studio preset) shape tests. Verifies that:
#   - Defaults compose cleanly (happy path).
#   - The domain name is project-prefixed (inspector attribution path).
#   - The Project tag is on the domain (defense-in-depth alongside the prefix).
#   - The execution role is created with the expected name.
#   - The workspace bucket is preset-created when not supplied.
#   - VPC mode flips app_network_access_type when vpc_id + subnet_ids set.
#   - Caller-supplied workspace_bucket suppresses the preset's S3 resources.
#   - Validation triggers fire on malformed user names, invalid policy ARN,
#     invalid bucket name, and the cross-attr vpc_id + empty subnet_ids gate.

# -----------------------------------------------------------------------------
# Positive companion: minimum inputs plans cleanly.
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
}

# -----------------------------------------------------------------------------
# Workspace bucket: preset-created by default.
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
    condition     = aws_s3_bucket.workspace[0].bucket == "test-sagemaker-workspace"
    error_message = "Preset-created bucket name must be project-prefixed (`<project>-sagemaker-workspace`)."
  }

  assert {
    condition     = length(aws_s3_bucket_public_access_block.workspace) == 1
    error_message = "Public-access-block resource must exist on the preset-created workspace bucket (security default)."
  }
}

# -----------------------------------------------------------------------------
# Workspace bucket: caller-supplied → preset skips its own bucket.
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
# VPC mode: vpc_id + subnet_ids → VpcOnly.
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
# Studio user profiles: for_each over var.studio_users.
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
# Negative cases — validation must reject obvious misconfigurations at plan
# time so callers don't discover them at apply.
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

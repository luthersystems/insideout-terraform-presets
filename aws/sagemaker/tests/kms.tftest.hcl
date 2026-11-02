mock_provider "aws" {}
mock_provider "random" {}

# Issue #909: opt-in customer-managed KMS key for the SageMaker workspace
# bucket. The default must stay SSE-S3 (AES256) with no KMS grant on the
# execution role, so live workspaces keep the same plan. When the key is
# set, the execution role must get the KMS grant it needs to read and
# write SSE-KMS objects, or Studio breaks on its own bucket.

run "sagemaker_default_workspace_is_sse_s3" {
  command = plan

  variables {
    project    = "test"
    vpc_id     = "vpc-12345"
    subnet_ids = ["subnet-aaa"]
  }

  assert {
    condition     = one([for r in aws_s3_bucket_server_side_encryption_configuration.workspace[0].rule : one(r.apply_server_side_encryption_by_default).sse_algorithm]) == "AES256"
    error_message = "With no workspace_kms_key_arn the workspace bucket must stay SSE-S3 (AES256) (#909)."
  }

  assert {
    condition     = length(aws_iam_role_policy.studio_workspace_kms) == 0
    error_message = "With no workspace_kms_key_arn the execution role must get no KMS grant."
  }
}

run "sagemaker_workspace_kms_key" {
  command = plan

  variables {
    project               = "test"
    vpc_id                = "vpc-12345"
    subnet_ids            = ["subnet-aaa"]
    workspace_kms_key_arn = "arn:aws:kms:us-east-1:123456789012:key/11111111-2222-3333-4444-555555555555"
  }

  assert {
    condition     = one([for r in aws_s3_bucket_server_side_encryption_configuration.workspace[0].rule : one(r.apply_server_side_encryption_by_default).sse_algorithm]) == "aws:kms"
    error_message = "workspace_kms_key_arn must switch the workspace bucket to SSE-KMS."
  }

  assert {
    condition     = one([for r in aws_s3_bucket_server_side_encryption_configuration.workspace[0].rule : one(r.apply_server_side_encryption_by_default).kms_master_key_id]) == "arn:aws:kms:us-east-1:123456789012:key/11111111-2222-3333-4444-555555555555"
    error_message = "workspace_kms_key_arn must pass through to kms_master_key_id."
  }

  assert {
    condition = anytrue([
      for st in jsondecode(aws_iam_role_policy.studio_workspace_kms[0].policy).Statement :
      st.Resource == "arn:aws:kms:us-east-1:123456789012:key/11111111-2222-3333-4444-555555555555" && contains(st.Action, "kms:Decrypt") && contains(st.Action, "kms:GenerateDataKey")
    ])
    error_message = "With workspace_kms_key_arn the execution role must get kms:Decrypt and kms:GenerateDataKey on the key, or it cannot read or write its SSE-KMS workspace."
  }
}

run "sagemaker_caller_bucket_kms_grant_only" {
  command = plan

  variables {
    project               = "test"
    vpc_id                = "vpc-12345"
    subnet_ids            = ["subnet-aaa"]
    workspace_bucket      = "caller-bucket"
    workspace_kms_key_arn = "arn:aws:kms:us-east-1:123456789012:key/11111111-2222-3333-4444-555555555555"
  }

  assert {
    condition     = length(aws_s3_bucket_server_side_encryption_configuration.workspace) == 0
    error_message = "A caller-supplied bucket keeps its own encryption; the preset must not create an encryption config for it."
  }

  assert {
    condition     = length(aws_iam_role_policy.studio_workspace_kms) == 1
    error_message = "A caller-supplied SSE-KMS bucket still needs the KMS grant on the execution role."
  }
}

run "sagemaker_rejects_non_kms_arn" {
  command = plan

  variables {
    project               = "test"
    vpc_id                = "vpc-12345"
    subnet_ids            = ["subnet-aaa"]
    workspace_kms_key_arn = "not-an-arn"
  }

  expect_failures = [var.workspace_kms_key_arn]
}

# S3 can use only a KMS key in the bucket's own region; the precondition
# moves the apply-time failure to plan time.
run "sagemaker_rejects_key_in_other_region" {
  command = plan

  variables {
    project               = "test"
    region                = "us-east-1"
    vpc_id                = "vpc-12345"
    subnet_ids            = ["subnet-aaa"]
    workspace_kms_key_arn = "arn:aws:kms:eu-west-2:123456789012:key/11111111-2222-3333-4444-555555555555"
  }

  expect_failures = [aws_s3_bucket_server_side_encryption_configuration.workspace]
}

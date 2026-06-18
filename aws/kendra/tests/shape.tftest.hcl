# Plan-only unit tests for the kendra module.
#
# No AWS credentials required: the AWS provider is mocked so
# data.aws_caller_identity.current / data.aws_region.current / data.aws_partition
# resolve at plan time, and the aws_kendra_index resource is mocked so the
# computed id/arn the data source + access policy reference resolve.
#
# These run in CI (filename has no "integration"). Run locally with:
#
#   cd aws/kendra
#   terraform init
#   terraform test -filter=tests/shape.tftest.hcl
#
# A live-apply integration suite is deliberately deferred: a Kendra index takes
# ~30 minutes to provision and a DEVELOPER_EDITION index bills ~$1.125/hr for
# the life of the test, so a live-apply test is expensive and slow relative to
# the value it adds over this shape suite. TODO(#760): add an opt-in
# integration.tftest.hcl (creds-gated, like aws/bedrock_agent) if a live
# regression is ever needed.

mock_provider "aws" {
  mock_data "aws_caller_identity" {
    defaults = {
      account_id = "111111111111"
      arn        = "arn:aws:iam::111111111111:user/test"
      user_id    = "AIDACKCEVSQ6C2EXAMPLE"
    }
  }
  mock_data "aws_region" {
    defaults = {
      region = "us-east-1"
    }
  }
  mock_data "aws_partition" {
    defaults = {
      partition = "aws"
    }
  }
  # The data source + access policy reference the index id/arn; give the index
  # resource id/arn-shaped mocks so those attributes resolve.
  mock_resource "aws_kendra_index" {
    defaults = {
      id  = "11111111-2222-3333-4444-555555555555"
      arn = "arn:aws:kendra:us-east-1:111111111111:index/11111111-2222-3333-4444-555555555555"
    }
  }
  mock_resource "aws_iam_role" {
    defaults = {
      arn = "arn:aws:iam::111111111111:role/iotkdr-kendra-index-role"
    }
  }
}

variables {
  project     = "iotkdr"
  region      = "us-east-1"
  environment = "test"
}

# --- Default shape (index only, no S3 data source) ---------------------------

run "defaults" {
  command = plan

  # Index role is always created (the index requires role_arn).
  assert {
    condition     = aws_iam_role.index.name == "iotkdr-kendra-index-role"
    error_message = "Index IAM role must always be created with the project-prefixed name."
  }

  # Trust policy carries the confused-deputy guards.
  assert {
    condition     = jsondecode(aws_iam_role.index.assume_role_policy).Statement[0].Condition.StringEquals["aws:SourceAccount"] == "111111111111"
    error_message = "Index trust policy must scope aws:SourceAccount to the current account."
  }

  assert {
    condition     = jsondecode(aws_iam_role.index.assume_role_policy).Statement[0].Principal.Service == "kendra.amazonaws.com"
    error_message = "Index trust policy must trust the kendra service principal."
  }

  assert {
    condition     = can(jsondecode(aws_iam_role.index.assume_role_policy).Statement[0].Condition.ArnLike["aws:SourceArn"])
    error_message = "Index trust policy must include an aws:SourceArn ArnLike condition scoping trust to this account's indexes."
  }

  # The CloudWatch policy scopes PutMetricData to the AWS/Kendra namespace.
  assert {
    condition     = jsondecode(aws_iam_role_policy.index_cloudwatch.policy).Statement[0].Condition.StringEquals["cloudwatch:namespace"] == "AWS/Kendra"
    error_message = "Index CloudWatch policy must scope PutMetricData to the AWS/Kendra namespace."
  }

  # The index itself is always created with the defaulted name + edition.
  assert {
    condition     = aws_kendra_index.this.name == "iotkdr-index"
    error_message = "Index name must default to {project}-index."
  }

  assert {
    condition     = aws_kendra_index.this.edition == "DEVELOPER_EDITION"
    error_message = "Index must default to DEVELOPER_EDITION."
  }

  assert {
    condition     = aws_kendra_index.this.user_context_policy == "ATTRIBUTE_FILTER"
    error_message = "Index must default to the ATTRIBUTE_FILTER user_context_policy."
  }

  # No KMS key wired: the SSE block is omitted (Kendra uses an AWS-owned key).
  assert {
    condition     = length(aws_kendra_index.this.server_side_encryption_configuration) == 0
    error_message = "Index must NOT emit a server_side_encryption_configuration block when no kms_key_id is wired."
  }

  # Default (no s3_bucket_name): no data source, no access role, no policy.
  assert {
    condition     = length(aws_kendra_data_source.s3) == 0
    error_message = "S3 data source must NOT be created when s3_bucket_name is null (default)."
  }

  assert {
    condition     = length(aws_iam_role.data_source) == 0
    error_message = "S3 data-source access role must NOT be created when no bucket is wired."
  }

  assert {
    condition     = length(aws_iam_role_policy.data_source) == 0
    error_message = "S3 data-source access policy must NOT be created when no bucket is wired."
  }

  # Observability (#760): the DocumentsFailedToIndex alarm is emitted by
  # default (enable_observability defaults true) and keyed on the IndexId
  # dimension the AWS/Kendra namespace publishes under.
  assert {
    condition     = length(aws_cloudwatch_metric_alarm.documents_failed_to_index_high) == 1
    error_message = "DocumentsFailedToIndex alarm must be emitted by default."
  }

  assert {
    condition     = aws_cloudwatch_metric_alarm.documents_failed_to_index_high["0"].metric_name == "DocumentsFailedToIndex"
    error_message = "The default alarm must watch the DocumentsFailedToIndex metric in the AWS/Kendra namespace."
  }
}

# --- Observability can be disabled -------------------------------------------

run "observability_disabled_suppresses_alarm" {
  command = plan

  variables {
    enable_observability = false
  }

  assert {
    condition     = length(aws_cloudwatch_metric_alarm.documents_failed_to_index_high) == 0
    error_message = "enable_observability=false must suppress the DocumentsFailedToIndex alarm."
  }
}

# --- With S3 data source (corpus wired in) -----------------------------------

run "with_s3_source" {
  # apply (not plan): the data source + access role reference
  # aws_kendra_index.this.id / .arn, computed attributes that are "(known after
  # apply)" at plan time — asserting on them under command = plan raises
  # "Unknown condition value". mock_provider populates the mocks at apply and
  # never calls AWS, so this stays credential-free (mirrors aws/bedrock's
  # command = apply for the same computed-attr-in-assertion reason).
  command = apply

  variables {
    s3_bucket_name = "iotkdr-docs"
    s3_bucket_arn  = "arn:aws:s3:::iotkdr-docs"
  }

  assert {
    condition     = length(aws_kendra_data_source.s3) == 1
    error_message = "S3 data source must be created when s3_bucket_name is supplied."
  }

  assert {
    condition     = aws_kendra_data_source.s3[0].type == "S3"
    error_message = "Data source must be of type S3."
  }

  assert {
    condition     = aws_kendra_data_source.s3[0].configuration[0].s3_configuration[0].bucket_name == "iotkdr-docs"
    error_message = "S3 data source must point at the wired bucket name."
  }

  # The access role + policy are created.
  assert {
    condition     = length(aws_iam_role.data_source) == 1
    error_message = "S3 data-source access role must be created alongside the data source."
  }

  assert {
    condition     = length(aws_iam_role_policy.data_source) == 1
    error_message = "S3 data-source access policy must be created alongside the data source."
  }

  # SECURITY (least privilege): GetObject is scoped to the wired bucket's
  # objects, not "*". A widened resource would be an over-grant.
  assert {
    condition     = jsondecode(aws_iam_role_policy.data_source[0].policy).Statement[0].Resource == "arn:aws:s3:::iotkdr-docs/*"
    error_message = "Data-source policy must scope s3:GetObject to the wired bucket's objects."
  }

  # SECURITY: ingest grants are scoped to THIS index's ARN, not all indexes.
  assert {
    condition     = jsondecode(aws_iam_role_policy.data_source[0].policy).Statement[2].Resource == "arn:aws:kendra:us-east-1:111111111111:index/11111111-2222-3333-4444-555555555555"
    error_message = "Data-source policy must scope the BatchPutDocument grant to this index's ARN."
  }

  # The data source binds the access role.
  assert {
    condition     = aws_kendra_data_source.s3[0].role_arn == aws_iam_role.data_source[0].arn
    error_message = "S3 data source must assume the dedicated access role."
  }
}

# --- Bucket ARN derived from name when not explicitly wired ------------------
#
# A single-module caller may pass only s3_bucket_name (no arn). The policy must
# still be least-privilege by deriving a partition-correct ARN from the name —
# guards against a regression that wildcards the resource when the arn is unset.

run "s3_arn_derived_from_name" {
  command = apply

  variables {
    s3_bucket_name = "iotkdr-docs"
  }

  assert {
    condition     = jsondecode(aws_iam_role_policy.data_source[0].policy).Statement[0].Resource == "arn:aws:s3:::iotkdr-docs/*"
    error_message = "When only the bucket name is wired, the policy must derive a least-privilege ARN from it."
  }
}

# --- KMS key wired emits the SSE block ---------------------------------------

run "kms_key_emits_sse_block" {
  command = plan

  variables {
    kms_key_id = "arn:aws:kms:us-east-1:111111111111:key/abc-123"
  }

  assert {
    condition     = length(aws_kendra_index.this.server_side_encryption_configuration) == 1
    error_message = "A wired kms_key_id must emit a server_side_encryption_configuration block."
  }

  assert {
    condition     = aws_kendra_index.this.server_side_encryption_configuration[0].kms_key_id == "arn:aws:kms:us-east-1:111111111111:key/abc-123"
    error_message = "The SSE block must carry the wired kms_key_id."
  }
}

# --- Enterprise edition + custom index name honored --------------------------

run "enterprise_edition_and_custom_name" {
  command = plan

  variables {
    edition             = "ENTERPRISE_EDITION"
    index_name          = "support-search"
    user_context_policy = "USER_TOKEN"
  }

  assert {
    condition     = aws_kendra_index.this.edition == "ENTERPRISE_EDITION"
    error_message = "Index must honor the explicit ENTERPRISE_EDITION."
  }

  assert {
    condition     = aws_kendra_index.this.name == "support-search"
    error_message = "Index must honor the explicit index_name override."
  }

  assert {
    condition     = aws_kendra_index.this.user_context_policy == "USER_TOKEN"
    error_message = "Index must honor the explicit USER_TOKEN user_context_policy."
  }
}

# --- GovCloud bucket ARN accepted --------------------------------------------
#
# s3_bucket_arn's regex (^arn:aws[a-zA-Z-]*:s3:::) deliberately admits
# aws-us-gov / aws-cn partitions. Confirm a GovCloud ARN is ACCEPTED and flows
# through to the access policy — guards against an over-tightening mutation
# (^arn:aws:s3:::) that would silently break GovCloud customers.

run "govcloud_bucket_arn_accepted" {
  command = apply

  variables {
    s3_bucket_name = "iotkdr-docs"
    s3_bucket_arn  = "arn:aws-us-gov:s3:::iotkdr-docs"
  }

  assert {
    condition     = jsondecode(aws_iam_role_policy.data_source[0].policy).Statement[0].Resource == "arn:aws-us-gov:s3:::iotkdr-docs/*"
    error_message = "A GovCloud-partition bucket ARN must flow through to the access policy."
  }
}

# --- Validation failures -----------------------------------------------------

run "bad_edition_rejected" {
  command = plan

  variables {
    edition = "GEN_AI_ENTERPRISE_EDITION"
  }

  expect_failures = [
    var.edition,
  ]
}

run "bad_user_context_policy_rejected" {
  command = plan

  variables {
    user_context_policy = "OPEN"
  }

  expect_failures = [
    var.user_context_policy,
  ]
}

run "bad_index_name_rejected" {
  command = plan

  variables {
    # Spaces are not permitted by the index_name regex.
    index_name = "not a valid name"
  }

  expect_failures = [
    var.index_name,
  ]
}

run "bad_bucket_name_rejected" {
  command = plan

  variables {
    # Uppercase is not permitted by the s3_bucket_name regex.
    s3_bucket_name = "INVALID_BUCKET"
  }

  expect_failures = [
    var.s3_bucket_name,
  ]
}

run "bad_bucket_arn_rejected" {
  command = plan

  variables {
    s3_bucket_name = "iotkdr-docs"
    s3_bucket_arn  = "not-an-arn"
  }

  expect_failures = [
    var.s3_bucket_arn,
  ]
}

# --- Module half of the count-on-computed fix (#807) -------------------------
#
# The composer gates the S3 data source on a plan-time-known enable_s3_data_source
# flag, not the (composed: computed) s3_bucket_name. Prove the preset honors it:
# with the flag false but a non-null bucket name supplied, NO data source /
# access role / policy may be created — count must follow the bool, not the name.
# A revert to `count = var.s3_bucket_name != null` makes this run fail.
run "enable_s3_data_source_false_overrides_name" {
  command = plan

  variables {
    s3_bucket_name        = "iotkdr-docs"
    enable_s3_data_source = false
  }

  assert {
    condition     = length(aws_kendra_data_source.s3) == 0
    error_message = "enable_s3_data_source=false must suppress the S3 data source even when s3_bucket_name is set."
  }

  assert {
    condition     = length(aws_iam_role.data_source) == 0
    error_message = "enable_s3_data_source=false must suppress the data-source access role."
  }

  assert {
    condition     = length(aws_iam_role_policy.data_source) == 0
    error_message = "enable_s3_data_source=false must suppress the data-source access policy."
  }
}

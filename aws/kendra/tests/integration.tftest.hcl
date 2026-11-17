# Integration tests for the kendra module — APPLIES AGAINST A REAL AWS ACCOUNT.
#
# This suite is opt-in. CI never runs it: terraform-validate.yml skips every
# *integration* .tftest.hcl file, because these need live AWS credentials and
# cost real money. The credential-free tests/shape.tftest.hcl is what CI runs.
#
# Run with valid AWS credentials in the target account:
#
#   cd aws/kendra
#   terraform init -backend=false
#   AWS_REGION=us-east-1 terraform test -filter=tests/integration.tftest.hcl
#
# Account access needed: permission to create Kendra indexes and data sources,
# IAM roles and inline policies, and S3 buckets in a region where Kendra is
# available (us-east-1 is used below).
#
# Time and cost: a Kendra index takes ~30 minutes to create and several more
# to delete, so expect the whole run to take 40-60 minutes. The index is
# DEVELOPER_EDITION, which bills ~$1.125/hour while it exists, so one run costs
# roughly $1-2. ENTERPRISE_EDITION is not tested here: it costs far more and
# exercises the same Terraform.
#
# What it does:
#   1. setup_docs_bucket: stands up a real S3 bucket via the sibling ../s3
#      preset so the S3 data source has a real bucket to crawl.
#   2. kendra_apply: applies kendra with the bucket wired in, asserting the
#      index comes up with an id and ARN on DEVELOPER_EDITION, the index role
#      keeps its confused-deputy trust guard, and the S3 data source plus its
#      access role are created and point at the live index and bucket. This is
#      the live-apply proof the shape suite cannot give: that the IAM
#      propagation sleep is long enough for CreateIndex / CreateDataSource to
#      accept the roles, and that the provider schema still matches the API.
#   3. Tears EVERYTHING down at the end (terraform test always destroys).
#
# Resource names embed a UTC timestamp suffix (MMDDhhmmss) under the iotkdr-
# prefix so back-to-back or concurrent runs do not collide on index, role or
# bucket names.
#
# If a prior run was killed mid-apply, manual cleanup may be required (an
# orphaned index keeps billing until it is deleted):
#
#   aws kendra list-indices --query 'IndexConfigurationSummaryItems[?starts_with(Name, `iotkdr-`)]'
#   aws kendra delete-index --id <id>
#   aws iam list-roles --query 'Roles[?starts_with(RoleName, `iotkdr-`)]'
#   # delete the inline policies, then:
#   aws iam delete-role --role-name <name>
#   aws s3 ls | grep iotkdr-
#   aws s3 rb s3://<bucket> --force

provider "aws" {
  region = var.region
}

variables {
  # Suffix the project name with a UTC MMDDhhmmss timestamp so runs do not
  # collide. Computed once at file-load time and shared by every run in this
  # file, so setup_docs_bucket and kendra_apply see the same project string.
  # Two runs in the exact same second would collide; acceptable for a
  # human-driven test.
  project     = "iotkdr-${formatdate("MMDDhhmmss", timestamp())}"
  region      = "us-east-1"
  environment = "test"

  # Pinned explicitly (it is also the default) so a future default change can
  # never turn this into a much more expensive ENTERPRISE_EDITION run.
  edition = "DEVELOPER_EDITION"
}

# --- Setup: real S3 document bucket ------------------------------------------
#
# Uses the sibling s3 preset so the data source has a concrete bucket. Path is
# resolved relative to the module under test (aws/kendra).
run "setup_docs_bucket" {
  command = apply

  module {
    source = "../s3"
  }

  variables {
    project     = var.project
    environment = var.environment
    region      = var.region

    # The bucket starts empty, but force_destroy keeps the final teardown
    # working if anyone drops test documents into it during a run.
    force_destroy = true
  }

  assert {
    condition     = output.bucket_name != null && length(output.bucket_name) > 0
    error_message = "Docs bucket setup must yield a bucket name."
  }
}

# --- Main: kendra against the real bucket ------------------------------------
run "kendra_apply" {
  command = apply

  variables {
    s3_bucket_name = run.setup_docs_bucket.bucket_name
    s3_bucket_arn  = run.setup_docs_bucket.bucket_arn
  }

  # --- Index up with id + arn on the cheap edition ---
  assert {
    condition     = aws_kendra_index.this.name == "${var.project}-index"
    error_message = "Index name must default to {project}-index."
  }

  assert {
    condition     = aws_kendra_index.this.edition == "DEVELOPER_EDITION"
    error_message = "Live index must be DEVELOPER_EDITION (the cost-friendly edition this test pins)."
  }

  assert {
    condition     = length(output.index_id) > 0
    error_message = "index_id output must be populated after a live apply."
  }

  assert {
    condition     = can(regex("^arn:aws[a-zA-Z-]*:kendra:", output.index_arn))
    error_message = "index_arn output must be a Kendra index ARN after a live apply."
  }

  # Kendra reports ACTIVE only once the index finished provisioning. Apply
  # returns after the provider's create waiter, so anything else means the
  # create did not really finish.
  assert {
    condition     = aws_kendra_index.this.status == "ACTIVE"
    error_message = "Live index must reach ACTIVE status after apply."
  }

  # --- Index role + confused-deputy trust guard ---
  assert {
    condition     = output.kendra_role_arn == aws_iam_role.index.arn
    error_message = "kendra_role_arn output must be the index role's ARN."
  }

  assert {
    condition     = jsondecode(aws_iam_role.index.assume_role_policy).Statement[0].Condition.StringEquals["aws:SourceAccount"] == data.aws_caller_identity.current.account_id
    error_message = "Index trust policy must scope aws:SourceAccount to the deploying account."
  }

  # --- S3 data source wired to the live index and bucket ---
  assert {
    condition     = length(aws_kendra_data_source.s3) == 1
    error_message = "S3 data source must be created when s3_bucket_name is wired in."
  }

  assert {
    condition     = aws_kendra_data_source.s3[0].index_id == aws_kendra_index.this.id
    error_message = "S3 data source must belong to the live index."
  }

  assert {
    condition     = aws_kendra_data_source.s3[0].configuration[0].s3_configuration[0].bucket_name == run.setup_docs_bucket.bucket_name
    error_message = "S3 data source must crawl the live setup bucket."
  }

  assert {
    condition     = output.data_source_id != null && length(output.data_source_id) > 0
    error_message = "data_source_id output must be populated after a live data-source apply."
  }

  assert {
    condition     = output.data_source_role_arn == aws_iam_role.data_source[0].arn
    error_message = "data_source_role_arn output must be the data-source access role's ARN."
  }
}

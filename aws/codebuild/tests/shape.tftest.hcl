mock_provider "aws" {}
mock_provider "random" {}

# Issue #619 (aws/codebuild standalone preset) shape tests. Verifies:
#   - Defaults compose cleanly (minimum inputs happy path).
#   - Project name carries the var.project prefix (inspector attribution).
#   - Project tag is on every taggable resource (defense-in-depth alongside
#     the prefix, CLAUDE.md issue #81).
#   - Service role's trust policy is correctly scoped:
#       - allows codebuild.amazonaws.com
#       - carries the aws:SourceAccount confused-deputy guard
#       - never wildcards SourceAccount
#   - Optional S3 logs bucket toggles between off (no bucket / no S3
#     hardening trio) and on (bucket + versioning + AES256 + public-
#     access block ALL on, with `logs_config.s3_logs.location` wired).
#   - Optional VPC config dynamic block toggles on a non-empty
#     subnet_ids list (vpc_id / subnets / security_group_ids propagate).
#   - source_type / artifacts_type / buildspec propagate to the project.
#   - Validation rejects every misconfiguration axis, AND a positive
#     companion run pins that the negatives fire on the right rule
#     (rejection-axis triangulation per #614).
#
# Every run uses `command = plan`. The assertions below reference
# attributes the preset sets explicitly (names, tags, IAM trust-policy
# JSON, dynamic-block cardinality) — all plan-time-known. The
# mock_provider emits random strings for arn-shaped Computed fields
# which would fail apply-time validation on resources that demand
# ARN-shaped inputs (e.g. service_role on aws_codebuild_project) — same
# trade-off as aws/apprunner + aws/sagemaker shape tests. Every wiring
# cross-ref we care about is also pinned end-to-end in
# `TestComposeStack_AWSCodeBuild_Forward` in the Go composer wiring
# test.

# -----------------------------------------------------------------------------
# Positive: minimum inputs plan cleanly.
# -----------------------------------------------------------------------------

run "codebuild_minimum_inputs" {
  command = plan

  variables {
    project         = "test"
    source_location = "https://github.com/example/repo.git"
  }

  assert {
    condition     = aws_codebuild_project.main.name == "test-build"
    error_message = "project name must be `<project>-<codebuild_project_name>` so InsideOut name-prefix scoping attributes it to the stack."
  }

  assert {
    condition     = aws_codebuild_project.main.tags["Project"] == "test"
    error_message = "Project tag must be set on the CodeBuild project so the InsideOut inspector's exact-match filter sees it (CLAUDE.md issue #81)."
  }

  # Defaults propagate.
  assert {
    condition     = aws_codebuild_project.main.environment[0].compute_type == "BUILD_GENERAL1_SMALL"
    error_message = "Default compute_type must be BUILD_GENERAL1_SMALL."
  }

  assert {
    condition     = aws_codebuild_project.main.environment[0].image == "aws/codebuild/standard:7.0"
    error_message = "Default build_image must be aws/codebuild/standard:7.0."
  }

  assert {
    condition     = aws_codebuild_project.main.source[0].type == "GITHUB"
    error_message = "Default source_type must be GITHUB."
  }

  assert {
    condition     = aws_codebuild_project.main.artifacts[0].type == "NO_ARTIFACTS"
    error_message = "Default artifacts_type must be NO_ARTIFACTS."
  }

  # Service role exists, is project-prefixed.
  assert {
    condition     = aws_iam_role.service.name == "test-codebuild"
    error_message = "Service role must be project-prefixed."
  }

  assert {
    condition     = strcontains(aws_iam_role.service.assume_role_policy, "codebuild.amazonaws.com")
    error_message = "Service role assume_role_policy must trust codebuild.amazonaws.com."
  }

  assert {
    condition     = strcontains(aws_iam_role.service.assume_role_policy, "aws:SourceAccount")
    error_message = "Service role assume_role_policy must carry the aws:SourceAccount confused-deputy guard."
  }

  # Reject the wildcard-value form of the SourceAccount guard — a
  # mutation that hard-coded `"aws:SourceAccount": "*"` would still
  # satisfy the strcontains check above but effectively disable the
  # confused-deputy protection.
  assert {
    condition     = !strcontains(aws_iam_role.service.assume_role_policy, "\"aws:SourceAccount\":\"*\"")
    error_message = "SourceAccount guard must not be wildcarded (\"*\")."
  }

  # No logs bucket by default.
  assert {
    condition     = length(aws_s3_bucket.logs) == 0
    error_message = "Logs bucket must NOT be created when enable_s3_logs is unset (the default)."
  }

  assert {
    condition     = length(aws_s3_bucket_versioning.logs) == 0
    error_message = "Logs bucket versioning must NOT be created when enable_s3_logs is unset."
  }

  assert {
    condition     = length(aws_s3_bucket_server_side_encryption_configuration.logs) == 0
    error_message = "Logs bucket SSE configuration must NOT be created when enable_s3_logs is unset."
  }

  assert {
    condition     = length(aws_s3_bucket_public_access_block.logs) == 0
    error_message = "Logs bucket public-access block must NOT be created when enable_s3_logs is unset."
  }

  # No VPC config by default (empty subnet_ids list).
  assert {
    condition     = length(aws_codebuild_project.main.vpc_config) == 0
    error_message = "vpc_config dynamic block must be empty when subnet_ids is empty (the default)."
  }
}

# -----------------------------------------------------------------------------
# Positive: enable_s3_logs creates the bucket with full hardening.
# -----------------------------------------------------------------------------

run "codebuild_enable_s3_logs_creates_bucket" {
  command = plan

  variables {
    project         = "test"
    source_location = "https://github.com/example/repo.git"
    enable_s3_logs  = true
  }

  assert {
    condition     = length(aws_s3_bucket.logs) == 1
    error_message = "Logs bucket must be created when enable_s3_logs = true."
  }

  # Pins the project-prefix on the bucket name.
  assert {
    condition     = startswith(aws_s3_bucket.logs[0].bucket, "test-codebuild-logs-")
    error_message = "Logs bucket name must start with `<project>-codebuild-logs-` for inspector attribution + global uniqueness."
  }

  # Hardening trio: versioning + encryption + public-access block all on.
  assert {
    condition     = length(aws_s3_bucket_versioning.logs) == 1
    error_message = "Logs bucket versioning resource must be created when enable_s3_logs = true."
  }

  assert {
    condition     = aws_s3_bucket_versioning.logs[0].versioning_configuration[0].status == "Enabled"
    error_message = "Logs bucket versioning must be Enabled."
  }

  assert {
    condition     = length(aws_s3_bucket_server_side_encryption_configuration.logs) == 1
    error_message = "Logs bucket SSE configuration must be created when enable_s3_logs = true."
  }

  # The `rule` block is a set, not a list — can't index with [0].
  # Iterate via a for-expression instead to assert AES256 is present.
  assert {
    condition     = anytrue([for r in aws_s3_bucket_server_side_encryption_configuration.logs[0].rule : r.apply_server_side_encryption_by_default[0].sse_algorithm == "AES256"])
    error_message = "Logs bucket SSE algorithm must be AES256."
  }

  assert {
    condition     = length(aws_s3_bucket_public_access_block.logs) == 1
    error_message = "Logs bucket public-access block must be created when enable_s3_logs = true."
  }

  assert {
    condition     = aws_s3_bucket_public_access_block.logs[0].block_public_acls == true && aws_s3_bucket_public_access_block.logs[0].block_public_policy == true && aws_s3_bucket_public_access_block.logs[0].ignore_public_acls == true && aws_s3_bucket_public_access_block.logs[0].restrict_public_buckets == true
    error_message = "All four public-access block flags must be true (no partial public exposure)."
  }

  # s3_logs dynamic block on the project flips on.
  assert {
    condition     = length(aws_codebuild_project.main.logs_config[0].s3_logs) == 1
    error_message = "Project's logs_config.s3_logs dynamic block must be present when enable_s3_logs = true."
  }

  assert {
    condition     = aws_codebuild_project.main.logs_config[0].s3_logs[0].status == "ENABLED"
    error_message = "Project's logs_config.s3_logs.status must be ENABLED when enable_s3_logs = true."
  }
}

# -----------------------------------------------------------------------------
# Positive: subnet_ids non-empty toggles vpc_config block on.
# -----------------------------------------------------------------------------

run "codebuild_vpc_config_threads_inputs" {
  command = plan

  variables {
    project            = "test"
    source_location    = "https://github.com/example/repo.git"
    vpc_id             = "vpc-12345"
    subnet_ids         = ["subnet-aaa", "subnet-bbb"]
    security_group_ids = ["sg-12345"]
  }

  assert {
    condition     = length(aws_codebuild_project.main.vpc_config) == 1
    error_message = "vpc_config dynamic block must be present when subnet_ids is non-empty."
  }

  assert {
    condition     = aws_codebuild_project.main.vpc_config[0].vpc_id == "vpc-12345"
    error_message = "vpc_config.vpc_id must propagate var.vpc_id."
  }

  assert {
    condition     = length(aws_codebuild_project.main.vpc_config[0].subnets) == 2
    error_message = "vpc_config.subnets must propagate var.subnet_ids."
  }

  assert {
    condition     = length(aws_codebuild_project.main.vpc_config[0].security_group_ids) == 1
    error_message = "vpc_config.security_group_ids must propagate var.security_group_ids."
  }
}

# -----------------------------------------------------------------------------
# Positive: inline buildspec propagates to the project source block.
# -----------------------------------------------------------------------------

run "codebuild_buildspec_override_propagates" {
  command = plan

  variables {
    project         = "test"
    source_type     = "NO_SOURCE"
    source_location = ""
    buildspec       = "version: 0.2\nphases:\n  build:\n    commands:\n      - echo hello\n"
  }

  assert {
    condition     = aws_codebuild_project.main.source[0].buildspec == "version: 0.2\nphases:\n  build:\n    commands:\n      - echo hello\n"
    error_message = "Inline buildspec must propagate to aws_codebuild_project.source.buildspec verbatim."
  }

  # NO_SOURCE: source.location must be null (the preset coerces it).
  assert {
    condition     = aws_codebuild_project.main.source[0].location == null
    error_message = "source.location must be null when source_type = NO_SOURCE."
  }
}

# -----------------------------------------------------------------------------
# Positive: S3 artifacts toggle propagates location.
# -----------------------------------------------------------------------------

run "codebuild_artifacts_s3_propagates" {
  command = plan

  variables {
    project            = "test"
    source_location    = "https://github.com/example/repo.git"
    artifacts_type     = "S3"
    artifacts_location = "my-artifacts-bucket"
  }

  assert {
    condition     = aws_codebuild_project.main.artifacts[0].type == "S3"
    error_message = "artifacts.type must propagate var.artifacts_type."
  }

  assert {
    condition     = aws_codebuild_project.main.artifacts[0].location == "my-artifacts-bucket"
    error_message = "artifacts.location must propagate var.artifacts_location when artifacts_type = S3."
  }
}

# -----------------------------------------------------------------------------
# Positive triangulation companions for the rejection-axis negatives below.
# Pattern from #614 — pins that the negatives fire on the right validation
# rule, not a no-op short-circuit.
# -----------------------------------------------------------------------------

run "codebuild_valid_compute_type_plans_cleanly" {
  command = plan

  variables {
    project         = "test"
    source_location = "https://github.com/example/repo.git"
    compute_type    = "BUILD_GENERAL1_LARGE"
  }

  assert {
    condition     = aws_codebuild_project.main.environment[0].compute_type == "BUILD_GENERAL1_LARGE"
    error_message = "A valid compute_type must plan cleanly and propagate (companion to rejects_bad_compute_type)."
  }
}

run "codebuild_valid_source_type_plans_cleanly" {
  command = plan

  variables {
    project         = "test"
    source_type     = "CODECOMMIT"
    source_location = "https://git-codecommit.us-east-1.amazonaws.com/v1/repos/myrepo"
  }

  assert {
    condition     = aws_codebuild_project.main.source[0].type == "CODECOMMIT"
    error_message = "A valid source_type must plan cleanly and propagate (companion to rejects_bad_source_type)."
  }
}

run "codebuild_valid_artifacts_type_plans_cleanly" {
  command = plan

  variables {
    project            = "test"
    source_location    = "https://github.com/example/repo.git"
    artifacts_type     = "CODEPIPELINE"
    artifacts_location = ""
  }

  assert {
    condition     = aws_codebuild_project.main.artifacts[0].type == "CODEPIPELINE"
    error_message = "A valid artifacts_type must plan cleanly and propagate (companion to rejects_bad_artifacts_type)."
  }
}

run "codebuild_valid_project_name_plans_cleanly" {
  command = plan

  variables {
    project                = "test"
    codebuild_project_name = "my-build"
    source_location        = "https://github.com/example/repo.git"
  }

  assert {
    condition     = aws_codebuild_project.main.name == "test-my-build"
    error_message = "A regex-valid codebuild_project_name must plan cleanly (companion to rejects_bad_project_name)."
  }
}

# -----------------------------------------------------------------------------
# Negative cases — validation rejects obvious misconfigurations at plan
# time so callers don't discover them at apply. Each axis paired with a
# positive companion above.
# -----------------------------------------------------------------------------

run "codebuild_rejects_bad_compute_type" {
  command = plan

  variables {
    project         = "test"
    source_location = "https://github.com/example/repo.git"
    compute_type    = "BUILD_GENERAL1_HUGE"
  }

  expect_failures = [var.compute_type]
}

run "codebuild_rejects_bad_source_type" {
  command = plan

  variables {
    project         = "test"
    source_type     = "BITBUCKET"
    source_location = "https://bitbucket.org/example/repo.git"
  }

  expect_failures = [var.source_type]
}

run "codebuild_rejects_bad_artifacts_type" {
  command = plan

  variables {
    project         = "test"
    source_location = "https://github.com/example/repo.git"
    artifacts_type  = "GCS"
  }

  expect_failures = [var.artifacts_type]
}

run "codebuild_rejects_bad_project_name" {
  command = plan

  variables {
    project                = "test"
    codebuild_project_name = "has spaces"
    source_location        = "https://github.com/example/repo.git"
  }

  expect_failures = [var.codebuild_project_name]
}

run "codebuild_rejects_empty_project" {
  command = plan

  variables {
    project         = "   "
    source_location = "https://github.com/example/repo.git"
  }

  expect_failures = [var.project]
}

# Pins the 40-char project cap.
run "codebuild_rejects_oversized_project" {
  command = plan

  variables {
    # 41 chars — one over the limit. The trimspace-non-empty validation
    # passes; only the length validation should fire.
    project         = "abcdefghijklmnopqrstuvwxyz1234567890abcde"
    source_location = "https://github.com/example/repo.git"
  }

  expect_failures = [var.project]
}

# source_location + artifacts_location cross-variable validations
# (required-when-set) live at the AWS API layer rather than in
# variable validation, because Terraform variable validation evaluates
# each variable in isolation (the test framework rejects cross-`var.`
# references in a single rule). CodeBuild's apply-time validator
# rejects an empty source_location for any source_type other than
# NO_SOURCE, and an empty artifacts_location for artifacts_type = S3 —
# so the misconfiguration still surfaces, just at apply not at plan.

mock_provider "aws" {
  mock_data "aws_caller_identity" {
    defaults = { account_id = "111122223333" }
  }
  mock_data "aws_partition" {
    defaults = { partition = "aws" }
  }
}
mock_provider "random" {}

# Shape tests for aws/codepipeline (#936).

variables {
  project                     = "test"
  github_repository           = "acme/app"
  codebuild_project_name      = "test-build"
  codebuild_project_arn       = "arn:aws:codebuild:us-east-1:111122223333:project/test-build"
  codebuild_service_role_name = "test-codebuild"
}

run "github_default_creates_connection" {
  command = plan

  assert {
    condition     = length(aws_codestarconnections_connection.github) == 1
    error_message = "with no connection_arn, the preset must create the connection."
  }
  assert {
    condition     = aws_codestarconnections_connection.github[0].provider_type == "GitHub" && aws_codestarconnections_connection.github[0].tags["Project"] == "test"
    error_message = "connection must be a tagged GitHub connection."
  }
  assert {
    condition     = aws_codepipeline.main.name == "test-pipeline" && aws_codepipeline.main.pipeline_type == "V2"
    error_message = "pipeline must be `<project>-<pipeline_name>`, type V2."
  }
  assert {
    condition     = one([for s in aws_codepipeline.main.stage : one(s.action).provider if s.name == "Source"]) == "CodeStarSourceConnection"
    error_message = "a GITHUB source must use the CodeStar connection provider."
  }
  assert {
    condition     = one([for s in aws_codepipeline.main.stage : one(s.action).configuration.FullRepositoryId if s.name == "Source"]) == "acme/app"
    error_message = "the source must name the repository."
  }
  assert {
    condition     = one([for s in aws_codepipeline.main.stage : one(s.action).configuration.ProjectName if s.name == "Build"]) == "test-build"
    error_message = "the Build stage must run the wired CodeBuild project."
  }
  assert {
    condition     = output.next_step != ""
    error_message = "a new connection needs a console approval, and next_step must say so."
  }
  assert {
    condition     = aws_iam_role.pipeline.path == "/service-role/" && aws_iam_role.pipeline.permissions_boundary == null
    error_message = "with no iam_scope, the role keeps the default path and no boundary."
  }
  assert {
    condition     = jsondecode(aws_iam_role.pipeline.assume_role_policy).Statement[0].Condition.StringEquals["aws:SourceAccount"] == "111122223333"
    error_message = "trust must carry the aws:SourceAccount guard."
  }
  assert {
    condition     = aws_s3_bucket_public_access_block.artifacts.block_public_policy && aws_s3_bucket_versioning.artifacts.versioning_configuration[0].status == "Enabled"
    error_message = "the artifact bucket must be versioned and block public access."
  }
  assert {
    condition     = aws_iam_role_policy.codebuild_artifacts.role == "test-codebuild"
    error_message = "the CodeBuild role must get access to the artifact bucket."
  }
}

run "github_existing_connection" {
  command = plan

  variables {
    connection_arn = "arn:aws:codestar-connections:us-east-1:111122223333:connection/abc"
  }

  assert {
    condition     = length(aws_codestarconnections_connection.github) == 0 && output.next_step == ""
    error_message = "an existing connection must not be created again and needs no step."
  }
  assert {
    condition     = one([for s in aws_codepipeline.main.stage : one(s.action).configuration.ConnectionArn if s.name == "Source"]) == "arn:aws:codestar-connections:us-east-1:111122223333:connection/abc"
    error_message = "the source must use the given connection."
  }
}

run "s3_source" {
  command = plan

  variables {
    source_type       = "S3"
    github_repository = ""
    source_s3_bucket  = "src-bucket"
    source_s3_key     = "app.zip"
  }

  assert {
    condition     = length(aws_codestarconnections_connection.github) == 0 && output.connection_arn == null
    error_message = "an S3 source needs no connection."
  }
  assert {
    condition     = one([for s in aws_codepipeline.main.stage : one(s.action).provider if s.name == "Source"]) == "S3"
    error_message = "an S3 source must use the S3 provider."
  }
  assert {
    condition     = one([for s in aws_codepipeline.main.stage : one(s.action).configuration.S3ObjectKey if s.name == "Source"]) == "app.zip"
    error_message = "the source must name the object."
  }
}

run "iam_scope" {
  command = plan

  variables {
    iam_scope = {
      path                 = "/insideout/test/"
      permissions_boundary = "arn:aws:iam::111122223333:policy/test-workload-boundary"
    }
  }

  assert {
    condition     = aws_iam_role.pipeline.path == "/insideout/test/" && aws_iam_role.pipeline.permissions_boundary == "arn:aws:iam::111122223333:policy/test-workload-boundary"
    error_message = "iam_scope must set the role path and boundary."
  }
}

run "github_without_repository_fails" {
  command = plan

  variables {
    github_repository = ""
  }

  expect_failures = [aws_codepipeline.main]
}

run "s3_without_object_fails" {
  command = plan

  variables {
    source_type = "S3"
  }

  expect_failures = [aws_codepipeline.main]
}

run "bad_source_type_fails" {
  command = plan

  variables {
    source_type = "CODECOMMIT"
  }

  expect_failures = [var.source_type]
}

run "default_sse_s3" {
  command = plan

  assert {
    condition     = one(one(aws_s3_bucket_server_side_encryption_configuration.artifacts.rule).apply_server_side_encryption_by_default).sse_algorithm == "AES256"
    error_message = "with no kms_key_arn, the artifact bucket keeps SSE-S3."
  }
  assert {
    condition     = length(one(aws_codepipeline.main.artifact_store).encryption_key) == 0
    error_message = "with no kms_key_arn, the artifact store names no key."
  }
}

run "kms_key" {
  command = plan

  variables {
    kms_key_arn = "arn:aws:kms:us-east-1:111122223333:key/1234abcd-12ab-34cd-56ef-1234567890ab"
  }

  assert {
    condition     = one(one(aws_s3_bucket_server_side_encryption_configuration.artifacts.rule).apply_server_side_encryption_by_default).sse_algorithm == "aws:kms"
    error_message = "kms_key_arn must turn on SSE-KMS on the artifact bucket."
  }
  assert {
    condition     = one(one(aws_s3_bucket_server_side_encryption_configuration.artifacts.rule).apply_server_side_encryption_by_default).kms_master_key_id == var.kms_key_arn
    error_message = "the bucket must use kms_key_arn."
  }
  assert {
    condition     = one(one(aws_codepipeline.main.artifact_store).encryption_key).id == var.kms_key_arn && one(one(aws_codepipeline.main.artifact_store).encryption_key).type == "KMS"
    error_message = "the artifact store must encrypt with kms_key_arn."
  }
}

run "kms_alias_fails" {
  command = plan

  variables {
    kms_key_arn = "arn:aws:kms:us-east-1:111122223333:alias/build"
  }

  expect_failures = [var.kms_key_arn]
}

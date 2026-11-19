# AWS CodePipeline preset (#936).
#
# Peer of gcp/cloud_deploy for AWS. A two-stage pipeline: Source, then
# Build on the CodeBuild project of the aws/codebuild preset (the
# composer adds aws_codebuild to the stack and wires its outputs). The
# preset provisions:
#
#   - An artifact bucket (`aws_s3_bucket.artifacts`), hardened like
#     aws/codebuild's logs bucket.
#   - A service role (`aws_iam_role.pipeline`) that CodePipeline
#     assumes, with an inline policy: the artifact bucket, start and read
#     builds of the one CodeBuild project, and use of the one connection.
#   - An inline policy on the CodeBuild service role that lets the build
#     read and write the artifact bucket, which a CODEPIPELINE build needs.
#   - Source:
#       - source_type = "GITHUB" (default): a CodeStar connection
#         (`aws_codestarconnections_connection.github`) unless
#         `connection_arn` names an existing one. A new connection is
#         PENDING until a person approves it once in the AWS console
#         (Developer Tools > Settings > Connections). Until then the
#         pipeline runs fail at the Source stage. The output
#         `next_step` says so.
#       - source_type = "S3": a versioned object, with no console step.
#   - The pipeline (`aws_codepipeline.main`, type V2).

terraform {
  required_version = ">= 1.5"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 6.0"
    }
    random = {
      source  = "hashicorp/random"
      version = ">= 3.5"
    }
  }
}

module "name" {
  source         = "github.com/luthersystems/tf-modules.git//luthername?ref=v55.15.0"
  luther_project = var.project
  aws_region     = var.region
  luther_env     = var.environment
  org_name       = "luthersystems"
  component      = "insideout"
  subcomponent   = "cp"
  resource       = "cp"
}

data "aws_caller_identity" "current" {}

data "aws_partition" "current" {}

locals {
  tags = merge(module.name.tags, var.tags)

  pipeline_name     = "${var.project}-${var.pipeline_name}"
  is_github         = var.source_type == "GITHUB"
  create_connection = local.is_github && var.connection_arn == ""
  connection_arn = (
    !local.is_github ? null :
    local.create_connection ? aws_codestarconnections_connection.github[0].arn :
    var.connection_arn
  )
  source_bucket_arn = "arn:${data.aws_partition.current.partition}:s3:::${var.source_s3_bucket}"
}

# -----------------------------------------------------------------------------
# Artifact bucket.
# -----------------------------------------------------------------------------

resource "random_id" "artifacts_suffix" {
  byte_length = 3
}

resource "aws_s3_bucket" "artifacts" {
  bucket        = "${var.project}-codepipeline-${random_id.artifacts_suffix.hex}"
  force_destroy = true

  tags = merge(local.tags, {
    Name = "${var.project}-codepipeline-artifacts"
  })
}

resource "aws_s3_bucket_versioning" "artifacts" {
  bucket = aws_s3_bucket.artifacts.id
  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "artifacts" {
  bucket = aws_s3_bucket.artifacts.id
  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

resource "aws_s3_bucket_public_access_block" "artifacts" {
  bucket                  = aws_s3_bucket.artifacts.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

# -----------------------------------------------------------------------------
# Source connection.
# -----------------------------------------------------------------------------

resource "aws_codestarconnections_connection" "github" {
  count = local.create_connection ? 1 : 0

  name          = substr("${var.project}-github", 0, 32)
  provider_type = "GitHub"
  tags          = local.tags
}

# -----------------------------------------------------------------------------
# Service role.
# -----------------------------------------------------------------------------

resource "aws_iam_role" "pipeline" {
  name = "${var.project}-codepipeline"
  path = try(var.iam_scope.path, "/service-role/")

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "codepipeline.amazonaws.com" }
      Action    = "sts:AssumeRole"
      Condition = {
        StringEquals = { "aws:SourceAccount" = data.aws_caller_identity.current.account_id }
      }
    }]
  })

  permissions_boundary = try(var.iam_scope.permissions_boundary, null)
  tags                 = local.tags
}

resource "aws_iam_role_policy" "pipeline" {
  name = "${var.project}-codepipeline"
  role = aws_iam_role.pipeline.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = concat(
      [
        {
          Sid      = "Artifacts"
          Effect   = "Allow"
          Action   = ["s3:GetObject", "s3:GetObjectVersion", "s3:PutObject", "s3:GetBucketVersioning", "s3:GetBucketLocation"]
          Resource = [aws_s3_bucket.artifacts.arn, "${aws_s3_bucket.artifacts.arn}/*"]
        },
        {
          Sid      = "Build"
          Effect   = "Allow"
          Action   = ["codebuild:StartBuild", "codebuild:BatchGetBuilds"]
          Resource = [var.codebuild_project_arn]
        },
      ],
      local.is_github ? [{
        Sid    = "UseConnection"
        Effect = "Allow"
        # The service accepts either prefix since the CodeConnections rename.
        Action   = ["codestar-connections:UseConnection", "codeconnections:UseConnection"]
        Resource = [local.connection_arn]
      }] : [],
      local.is_github ? [] : [{
        Sid      = "SourceObject"
        Effect   = "Allow"
        Action   = ["s3:GetObject", "s3:GetObjectVersion", "s3:GetBucketVersioning"]
        Resource = [local.source_bucket_arn, "${local.source_bucket_arn}/${var.source_s3_key}"]
      }],
    )
  })
}

# A CODEPIPELINE build reads its input artifact from the bucket and writes
# its output there. The CodeBuild preset's role has no S3 access to it.
resource "aws_iam_role_policy" "codebuild_artifacts" {
  name = "${var.project}-codepipeline-artifacts"
  role = var.codebuild_service_role_name

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Sid      = "PipelineArtifacts"
      Effect   = "Allow"
      Action   = ["s3:GetObject", "s3:GetObjectVersion", "s3:PutObject", "s3:GetBucketLocation"]
      Resource = [aws_s3_bucket.artifacts.arn, "${aws_s3_bucket.artifacts.arn}/*"]
    }]
  })
}

# -----------------------------------------------------------------------------
# Pipeline.
# -----------------------------------------------------------------------------

resource "aws_codepipeline" "main" {
  name          = local.pipeline_name
  role_arn      = aws_iam_role.pipeline.arn
  pipeline_type = "V2"

  artifact_store {
    location = aws_s3_bucket.artifacts.bucket
    type     = "S3"
  }

  stage {
    name = "Source"

    action {
      name             = "Source"
      category         = "Source"
      owner            = "AWS"
      provider         = local.is_github ? "CodeStarSourceConnection" : "S3"
      version          = "1"
      output_artifacts = ["source"]

      configuration = local.is_github ? {
        ConnectionArn    = local.connection_arn
        FullRepositoryId = var.github_repository
        BranchName       = var.branch
        DetectChanges    = tostring(var.detect_changes)
        } : {
        S3Bucket             = var.source_s3_bucket
        S3ObjectKey          = var.source_s3_key
        PollForSourceChanges = tostring(var.detect_changes)
      }
    }
  }

  stage {
    name = "Build"

    action {
      name             = "Build"
      category         = "Build"
      owner            = "AWS"
      provider         = "CodeBuild"
      version          = "1"
      input_artifacts  = ["source"]
      output_artifacts = ["build"]

      configuration = {
        ProjectName = var.codebuild_project_name
      }
    }
  }

  tags = local.tags

  depends_on = [aws_iam_role_policy.pipeline]

  lifecycle {
    precondition {
      condition     = local.is_github ? can(regex("^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$", var.github_repository)) : true
      error_message = "source_type GITHUB needs github_repository in OWNER/REPO form."
    }
    precondition {
      condition     = local.is_github ? true : (var.source_s3_bucket != "" && var.source_s3_key != "")
      error_message = "source_type S3 needs source_s3_bucket and source_s3_key."
    }
  }
}

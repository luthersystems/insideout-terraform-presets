# AWS CodeBuild project preset (#619, deferred row 3 of #598).
#
# Mirrors the role gcp/cloud_build plays for GCP stacks: a single-call
# entry point for a managed build/CI runner on AWS. The preset
# provisions:
#
#   - A CodeBuild project (`aws_codebuild_project`) named
#     `${var.project}-${var.codebuild_project_name}` so the InsideOut
#     inspector can attribute it to the stack via name-prefix scoping
#     (CLAUDE.md "name-prefix scoping" rule).
#   - A service IAM role (`aws_iam_role.service`) the CodeBuild control
#     plane assumes to run builds. Trusts `codebuild.amazonaws.com` and
#     carries the `aws:SourceAccount` confused-deputy guard
#     (matches aws/apprunner + aws/sagemaker + aws/bedrock).
#   - An inline IAM role policy (`aws_iam_role_policy.service`) granting
#     the minimal permissions a build needs: CloudWatch Logs creation /
#     write for build logs, ECR pull for the runtime image, and — when
#     the optional VPC config is on — the EC2 ENI lifecycle calls
#     CodeBuild makes per-build to wire its build container into the
#     customer VPC. Inline (not attached managed policy) so the role's
#     permission lifecycle tracks 1:1 with the role; mirrors
#     aws/sagemaker's inline studio_workspace_access policy.
#   - Optional S3 logs bucket
#     (`aws_s3_bucket.logs` + versioning + AES256 + public-access block)
#     when `enable_s3_logs = true`. The bucket carries the standard
#     hardening trio matching aws/sagemaker's workspace bucket.
#   - Optional `vpc_config` block on the project, gated on a non-empty
#     `subnet_ids` list. The caller supplies `vpc_id`, `subnet_ids`, and
#     `security_group_ids` — the composer wires `vpc_id` + `subnet_ids`
#     from `module.aws_vpc` automatically when KeyAWSVPC is selected
#     (and is the ImplicitDependencies default for KeyAWSCodeBuild).
#
# Scope ceiling: matches gcp/cloud_build simplicity. CodeBuild batch
# build configurations, source-credential management
# (`aws_codebuild_source_credential`), report groups, fleet (compute
# pool) management, and webhook auto-trigger setup are exposed only as
# the AWS provider defaults — advanced consumers wrap or fork.
#
# Discovery inspector: deferred per #619 (issue explicitly marks the
# discovery inspector as out-of-scope / follow-up — mirrors the
# apprunner + sagemaker deferral pattern). See
# `pkg/observability/extractors/extractors_drift_test.go` allowlist.

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

# Standard luthername-driven naming + tag set. Every AWS preset in the
# repo wires through this module so the InsideOut inspector's exact
# `Project = <project>` filter (CLAUDE.md issue #81) catches every
# resource.
module "name" {
  source         = "github.com/luthersystems/tf-modules.git//luthername?ref=v55.15.0"
  luther_project = var.project
  aws_region     = var.region
  luther_env     = var.environment
  org_name       = "luthersystems"
  component      = "insideout"
  subcomponent   = "cb"
  resource       = "cb"
}

# Caller identity drives the aws:SourceAccount confused-deputy guard on
# the CodeBuild service-role trust policy (matches aws/apprunner +
# aws/sagemaker + aws/bedrock).
data "aws_caller_identity" "current" {}

locals {
  # Project-tag merge from the luthername module so every taggable
  # resource carries the full standard tag set.
  tags = merge(module.name.tags, var.tags)

  project_name = "${var.project}-${var.codebuild_project_name}"

  create_logs_bucket = var.enable_s3_logs
  # VPC config is opt-in via subnet_ids — leaving subnet_ids empty
  # leaves the dynamic block off so the project runs in the CodeBuild
  # public network plane (matches the default behaviour the AWS
  # console offers).
  needs_vpc_config = length(var.subnet_ids) > 0
}

# -----------------------------------------------------------------------------
# Optional S3 logs bucket.
#
# Standard hardening trio: versioning + AES256 + public-access block.
# Bucket name is suffixed with a random_id so distinct stacks don't
# collide on the global S3 namespace. Pattern mirrors aws/sagemaker's
# workspace bucket.
# -----------------------------------------------------------------------------

resource "random_id" "logs_suffix" {
  count       = local.create_logs_bucket ? 1 : 0
  byte_length = 3
}

resource "aws_s3_bucket" "logs" {
  count = local.create_logs_bucket ? 1 : 0

  bucket        = "${var.project}-codebuild-logs-${random_id.logs_suffix[0].hex}"
  force_destroy = true

  tags = merge(local.tags, {
    Name = "${var.project}-codebuild-logs"
  })
}

resource "aws_s3_bucket_versioning" "logs" {
  count = local.create_logs_bucket ? 1 : 0

  bucket = aws_s3_bucket.logs[0].id
  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "logs" {
  count = local.create_logs_bucket ? 1 : 0

  bucket = aws_s3_bucket.logs[0].id
  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

resource "aws_s3_bucket_public_access_block" "logs" {
  count = local.create_logs_bucket ? 1 : 0

  bucket                  = aws_s3_bucket.logs[0].id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

# -----------------------------------------------------------------------------
# IAM service role — assumed by the CodeBuild control plane per build.
# -----------------------------------------------------------------------------

resource "aws_iam_role" "service" {
  name = "${var.project}-codebuild"
  path = "/service-role/"

  # codebuild.amazonaws.com is the CodeBuild service principal. Scoped
  # to the deploying account via aws:SourceAccount so a hostile cross-
  # account caller can't trick CodeBuild into using this role on their
  # behalf.
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Principal = {
        Service = "codebuild.amazonaws.com"
      }
      Action = "sts:AssumeRole"
      Condition = {
        StringEquals = {
          "aws:SourceAccount" = data.aws_caller_identity.current.account_id
        }
      }
    }]
  })

  tags = local.tags

  lifecycle {
    ignore_changes = [managed_policy_arns]
  }
}

# Inline base policy: CloudWatch Logs (always on — every build emits
# log streams) + ECR pull (covers the default standard image on public
# ECR + any private image the caller swaps in). Inline (not attached
# managed policy) so the role's permission lifecycle tracks 1:1 with
# the role — mirrors aws/sagemaker's studio_workspace_access.
#
# The optional S3 logs + VPC ENI permission statements live on
# separate count-gated aws_iam_role_policy resources below so the
# policy doc itself stays a single-shape object literal and the
# permission set adapts cleanly to the enable_s3_logs / VPC toggles.
resource "aws_iam_role_policy" "service" {
  name = "${var.project}-codebuild"
  role = aws_iam_role.service.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      # CloudWatch Logs — every build emits a log stream.
      {
        Effect = "Allow"
        Action = [
          "logs:CreateLogGroup",
          "logs:CreateLogStream",
          "logs:PutLogEvents",
        ]
        Resource = "arn:aws:logs:${var.region}:${data.aws_caller_identity.current.account_id}:log-group:/aws/codebuild/${local.project_name}:*"
      },
      # ECR pull — the standard build image lives on a public ECR;
      # private images need ECR auth + pull.
      {
        Effect = "Allow"
        Action = [
          "ecr:BatchCheckLayerAvailability",
          "ecr:BatchGetImage",
          "ecr:GetDownloadUrlForLayer",
        ]
        Resource = "*"
      },
      # GetAuthorizationToken only accepts Resource "*" per the IAM
      # contract — kept on its own statement so the linter sees a
      # narrow Resource on the other ECR actions.
      {
        Effect   = "Allow"
        Action   = "ecr:GetAuthorizationToken"
        Resource = "*"
      },
    ]
  })
}

# Optional S3 logs access — only attached when enable_s3_logs = true so
# the role doesn't carry unused S3 permissions on builds that emit
# CloudWatch-only logs.
resource "aws_iam_role_policy" "service_s3_logs" {
  count = local.create_logs_bucket ? 1 : 0

  name = "${var.project}-codebuild-s3-logs"
  role = aws_iam_role.service.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Action = [
        "s3:GetObject",
        "s3:GetObjectVersion",
        "s3:PutObject",
        "s3:ListBucket",
      ]
      Resource = [
        aws_s3_bucket.logs[0].arn,
        "${aws_s3_bucket.logs[0].arn}/*",
      ]
    }]
  })
}

# Optional VPC ENI lifecycle — only attached when the project runs in
# a VPC (subnet_ids non-empty). CodeBuild creates an ENI per build in
# the provided subnets and deletes it when the build finishes;
# DescribeDhcpOptions is read by CodeBuild during VPC config
# validation. The ec2:CreateNetworkInterfacePermission action is
# scoped to the supplied subnets via the ec2:Subnet condition so the
# role can't attach ENIs in arbitrary subnets of the account.
resource "aws_iam_role_policy" "service_vpc" {
  count = local.needs_vpc_config ? 1 : 0

  name = "${var.project}-codebuild-vpc"
  role = aws_iam_role.service.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "ec2:CreateNetworkInterface",
          "ec2:DeleteNetworkInterface",
          "ec2:DescribeDhcpOptions",
          "ec2:DescribeNetworkInterfaces",
          "ec2:DescribeSecurityGroups",
          "ec2:DescribeSubnets",
          "ec2:DescribeVpcs",
        ]
        Resource = "*"
      },
      {
        Effect   = "Allow"
        Action   = "ec2:CreateNetworkInterfacePermission"
        Resource = "arn:aws:ec2:${var.region}:${data.aws_caller_identity.current.account_id}:network-interface/*"
        Condition = {
          StringEquals = {
            "ec2:AuthorizedService" = "codebuild.amazonaws.com"
          }
          ArnEquals = {
            "ec2:Subnet" = [for s in var.subnet_ids : "arn:aws:ec2:${var.region}:${data.aws_caller_identity.current.account_id}:subnet/${s}"]
          }
        }
      },
    ]
  })
}

# -----------------------------------------------------------------------------
# CodeBuild project.
# -----------------------------------------------------------------------------

resource "aws_codebuild_project" "main" {
  name         = local.project_name
  service_role = aws_iam_role.service.arn

  source {
    type      = var.source_type
    location  = var.source_type == "NO_SOURCE" ? null : var.source_location
    buildspec = var.buildspec
  }

  artifacts {
    type     = var.artifacts_type
    location = var.artifacts_type == "NO_ARTIFACTS" ? null : var.artifacts_location
  }

  environment {
    compute_type                = var.compute_type
    image                       = var.build_image
    type                        = "LINUX_CONTAINER"
    image_pull_credentials_type = "CODEBUILD"
    privileged_mode             = false
  }

  dynamic "vpc_config" {
    for_each = local.needs_vpc_config ? [1] : []
    content {
      vpc_id             = var.vpc_id
      subnets            = var.subnet_ids
      security_group_ids = var.security_group_ids
    }
  }

  logs_config {
    cloudwatch_logs {
      status = "ENABLED"
    }

    dynamic "s3_logs" {
      for_each = local.create_logs_bucket ? [1] : []
      content {
        status   = "ENABLED"
        location = "${aws_s3_bucket.logs[0].bucket}/build-logs/"
      }
    }
  }

  tags = local.tags
}

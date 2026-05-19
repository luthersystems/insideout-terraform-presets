# AWS SageMaker Studio domain preset (#615).
#
# Mirrors the role gcp/vertex_ai plays for GCP stacks: a single-call entry
# point for an ML workspace on AWS. The preset provisions:
#
#   - A SageMaker Studio domain (`aws_sagemaker_domain`) named
#     `${var.project}-studio` so the InsideOut inspector can attribute the
#     domain to the stack via name-prefix scoping (CLAUDE.md "name-prefix
#     scoping" rule — domains carry tags too, but the prefix is the
#     defense-in-depth path for label-less resource families).
#   - An IAM execution role (`aws_iam_role.studio_execution`) the
#     Studio apps assume when launching kernels / running training jobs.
#   - A workspace S3 bucket (preset-created by default; caller-supplied via
#     `var.workspace_bucket` for shared / cross-account buckets). The
#     execution role gets least-privilege Get/Put/List on this bucket.
#   - Optional Studio user profiles (`for_each = toset(var.studio_users)`).
#
# Scope ceiling: matches `gcp/vertex_ai` simplicity. Image / lifecycle
# config / KMS / VPC-mode networking modes are exposed but kept minimal —
# advanced consumers can wrap the module or fork.
#
# Discovery inspector: deferred per #615 (the issue explicitly marks the
# discovery inspector as optional follow-up). See
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

# Caller identity drives the aws:SourceAccount confused-deputy guard on
# the Studio execution role's trust policy (matches aws/bedrock).
data "aws_caller_identity" "current" {}

locals {
  # Standard Project-tag merge so the InsideOut inspector's exact
  # `Project = <project>` filter sees every resource (CLAUDE.md issue #81).
  tags = merge({ Project = var.project }, var.tags)

  # SageMaker domains can run in PublicInternetOnly or VpcOnly mode.
  # AWS provider 6.x requires `vpc_id` + `subnet_ids` on every domain
  # (they're non-nullable required arguments on the resource even though
  # PublicInternetOnly mode doesn't peer into the VPC). VpcOnly mode peers
  # into the customer VPC; PublicInternetOnly mode uses AWS-managed
  # networking but still needs the VPC reference for the studio app's
  # ENI placement. We flip the access mode based on var.network_mode so
  # callers can pick the right shape — both modes require the VPC inputs.
  vpc_only = var.network_mode == "VpcOnly"

  # Workspace bucket: preset-managed unless the caller supplies one. We
  # surface the resolved name as an output regardless of who owns the
  # bucket so callers can wire IAM policies / downstream consumers.
  # S3 bucket names are globally unique, so we suffix the preset-managed
  # bucket with a random_id to avoid project-name collisions when the
  # same stack is deployed twice in different accounts/regions.
  create_bucket         = var.workspace_bucket == null
  workspace_bucket_name = local.create_bucket ? aws_s3_bucket.workspace[0].id : var.workspace_bucket
  workspace_bucket_arn  = local.create_bucket ? aws_s3_bucket.workspace[0].arn : "arn:aws:s3:::${var.workspace_bucket}"
  default_bucket_name   = local.create_bucket ? "${var.project}-sagemaker-workspace-${random_id.workspace_suffix[0].hex}" : null
}

resource "random_id" "workspace_suffix" {
  count       = local.create_bucket ? 1 : 0
  byte_length = 3
}

# -----------------------------------------------------------------------------
# Workspace S3 bucket (preset-managed when var.workspace_bucket == null)
# -----------------------------------------------------------------------------

resource "aws_s3_bucket" "workspace" {
  count         = local.create_bucket ? 1 : 0
  bucket        = local.default_bucket_name
  force_destroy = var.workspace_bucket_force_destroy

  tags = merge(local.tags, { Name = local.default_bucket_name })
}

resource "aws_s3_bucket_versioning" "workspace" {
  count  = local.create_bucket ? 1 : 0
  bucket = aws_s3_bucket.workspace[0].id

  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "workspace" {
  count  = local.create_bucket ? 1 : 0
  bucket = aws_s3_bucket.workspace[0].id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

resource "aws_s3_bucket_public_access_block" "workspace" {
  count                   = local.create_bucket ? 1 : 0
  bucket                  = aws_s3_bucket.workspace[0].id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

# -----------------------------------------------------------------------------
# IAM execution role for SageMaker Studio apps
# -----------------------------------------------------------------------------

resource "aws_iam_role" "studio_execution" {
  name = "${var.project}-sagemaker-execution"
  path = "/service-role/"

  # Confused-deputy guard: scope the service trust to the deploying
  # account so a hostile cross-account caller can't trick the SageMaker
  # control plane into assuming this role on their behalf. Matches
  # aws/bedrock's bedrock_kb role + aws/route53's invocation_logging role.
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Principal = {
        Service = "sagemaker.amazonaws.com"
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

  # Managed policies attach via the sibling aws_iam_role_policy_attachment
  # block below; ignore_changes prevents drift noise when the provider
  # refresh re-reads the attached set onto the role attribute. Matches
  # aws/lambda's lambda_exec + aws/bedrock's bedrock_kb pattern.
  lifecycle {
    ignore_changes = [managed_policy_arns]
  }
}

# AmazonSageMakerFullAccess is broad — it permits the role to manage almost
# every SageMaker resource type plus a handful of supporting services
# (S3, ECR, CloudWatch). Trade-off: callers who need a locked-down
# environment must override via var.sagemaker_managed_policy_arn (e.g.
# point at an in-account scoped policy).
resource "aws_iam_role_policy_attachment" "studio_managed" {
  role       = aws_iam_role.studio_execution.name
  policy_arn = var.sagemaker_managed_policy_arn
}

# Workspace bucket access — least-privilege Get/Put/List on the resolved
# bucket. Inline policy (not a separate aws_iam_policy + attachment) so the
# permission lifecycle tracks the role 1:1; orphan policies on
# terraform destroy are impossible.
resource "aws_iam_role_policy" "studio_workspace_access" {
  name = "${var.project}-sagemaker-workspace-access"
  role = aws_iam_role.studio_execution.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "s3:GetObject",
          "s3:PutObject",
          "s3:DeleteObject",
        ]
        Resource = "${local.workspace_bucket_arn}/*"
      },
      {
        Effect = "Allow"
        Action = [
          "s3:ListBucket",
          "s3:GetBucketLocation",
        ]
        Resource = local.workspace_bucket_arn
      },
    ]
  })
}

# -----------------------------------------------------------------------------
# SageMaker Studio domain
# -----------------------------------------------------------------------------

resource "aws_sagemaker_domain" "studio" {
  # Name-prefix scoping per CLAUDE.md: even though the domain carries
  # tags, the prefix gives the inspector a deterministic attribution path
  # that's resilient to tag drift.
  domain_name             = "${var.project}-studio"
  auth_mode               = "IAM"
  vpc_id                  = var.vpc_id
  subnet_ids              = var.subnet_ids
  app_network_access_type = local.vpc_only ? "VpcOnly" : "PublicInternetOnly"

  default_user_settings {
    execution_role = aws_iam_role.studio_execution.arn
  }

  tags = local.tags

  # Cross-attr validation hosted as a precondition so the rule can
  # reference both var.vpc_id (non-null) and var.subnet_ids (non-empty)
  # together — Terraform 1.5+ disallows multi-variable refs in variable
  # validation blocks. Mirrors the gcp/github_actions all-empty-ref-gates
  # precondition pattern.
  lifecycle {
    precondition {
      condition     = length(trimspace(var.vpc_id)) > 0 && length(var.subnet_ids) > 0
      error_message = "vpc_id must be non-empty and subnet_ids must contain at least one subnet (SageMaker domains require a VPC + subnet pair in both VpcOnly and PublicInternetOnly modes)."
    }
  }
}

# -----------------------------------------------------------------------------
# Optional Studio user profiles (one per caller-supplied user name)
# -----------------------------------------------------------------------------

resource "aws_sagemaker_user_profile" "studio_user" {
  for_each = toset(var.studio_users)

  domain_id         = aws_sagemaker_domain.studio.id
  user_profile_name = each.value

  tags = local.tags
}

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

# Standard luthername-driven naming + tag set. Every AWS preset in the
# repo wires through this module so the InsideOut inspector's exact
# `Project = <project>` filter (CLAUDE.md issue #81) catches every
# resource, alongside the other standard tags the luthername module
# emits (env, component, region, etc.).
module "name" {
  source         = "github.com/luthersystems/tf-modules.git//luthername?ref=v55.15.0"
  luther_project = var.project
  aws_region     = var.region
  luther_env     = var.environment
  org_name       = "luthersystems"
  component      = "insideout"
  subcomponent   = "sm"
  resource       = "sm"
}

# Caller identity drives the aws:SourceAccount confused-deputy guard on
# the Studio execution role's trust policy (matches aws/bedrock).
data "aws_caller_identity" "current" {}

# Partition-aware ARN construction for the caller-supplied bucket fallback
# (aws / aws-us-gov / aws-cn). aws_s3_bucket.workspace[0].arn already
# carries the right partition for the preset-managed case.
data "aws_partition" "current" {}

locals {
  # Project-tag merge from the luthername module so every taggable
  # resource carries the full standard tag set (Project + env + region
  # + component + subcomponent + resource). var.tags overrides or
  # extends.
  tags = merge(module.name.tags, var.tags)

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
  workspace_bucket_arn  = local.create_bucket ? aws_s3_bucket.workspace[0].arn : "arn:${data.aws_partition.current.partition}:s3:::${var.workspace_bucket}"
  default_bucket_name   = local.create_bucket ? "${var.project}-sagemaker-workspace-${random_id.workspace_suffix[0].hex}" : null

  # Real-time inference slice (#761). When off, the preset stays Studio-only.
  # The model / endpoint-config / endpoint resources below use
  # `count = local.enable_inference ? 1 : 0` so they read cleanly as `[0]`
  # while staying absent when disabled.
  enable_inference = var.enable_inference

  # model_data_url is optional: many LLM serving images bundle / pull their
  # own weights. We only attach the s3:GetObject read grant for the artifact
  # when a URL is actually supplied (least privilege). Derive the bucket ARN
  # for the grant from the s3://bucket/key URL.
  model_data_bucket = local.enable_inference && trimspace(var.model_data_url) != "" ? split("/", replace(var.model_data_url, "s3://", ""))[0] : ""
  model_data_arn    = local.model_data_bucket == "" ? "" : "arn:${data.aws_partition.current.partition}:s3:::${local.model_data_bucket}"
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

# -----------------------------------------------------------------------------
# Real-time inference endpoint (#761) — gated on var.enable_inference.
#
# Lifecycle: aws_sagemaker_model (defines the servable container + the
# execution role it runs under) → aws_sagemaker_endpoint_configuration
# (the production-variant hosting plan: which model, instance type, count)
# → aws_sagemaker_endpoint (the live HTTPS endpoint InvokeEndpoint hits).
#
# The endpoint hosting container runs as the Studio execution role. To pull
# the model image from ECR and read the model artifact from S3 it needs
# explicit grants beyond Studio's bucket access — attached below, only when
# inference is enabled so the Studio-only path keeps a tighter role.
# -----------------------------------------------------------------------------

# ECR pull for the model image. ecr:GetAuthorizationToken is account-wide
# (Resource "*" is required — the token isn't scopable to a repo); the image
# layer/manifest reads are scoped to ECR repos in the deploying account.
resource "aws_iam_role_policy" "inference_ecr_pull" {
  count = local.enable_inference ? 1 : 0

  name = "${var.project}-sagemaker-inference-ecr-pull"
  role = aws_iam_role.studio_execution.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["ecr:GetAuthorizationToken"]
        Resource = "*"
      },
      {
        Effect = "Allow"
        Action = [
          "ecr:BatchCheckLayerAvailability",
          "ecr:GetDownloadUrlForLayer",
          "ecr:BatchGetImage",
        ]
        Resource = "arn:${data.aws_partition.current.partition}:ecr:${var.region}:${data.aws_caller_identity.current.account_id}:repository/*"
      },
    ]
  })
}

# S3 read for the model artifact (model.tar.gz). Only attached when a
# model_data_url is supplied — images that bundle their own weights need no
# extra S3 grant. Scoped to the specific artifact bucket (get object + list).
resource "aws_iam_role_policy" "inference_model_data" {
  count = local.enable_inference && local.model_data_arn != "" ? 1 : 0

  name = "${var.project}-sagemaker-inference-model-data"
  role = aws_iam_role.studio_execution.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["s3:GetObject"]
        Resource = "${local.model_data_arn}/*"
      },
      {
        Effect   = "Allow"
        Action   = ["s3:ListBucket"]
        Resource = local.model_data_arn
      },
    ]
  })
}

resource "aws_sagemaker_model" "inference" {
  count = local.enable_inference ? 1 : 0

  name               = "${var.project}-model"
  execution_role_arn = aws_iam_role.studio_execution.arn

  primary_container {
    image = var.model_image
    # model_data_url is optional — omit it (null) so the provider doesn't set
    # an empty ModelDataUrl, which AWS rejects. Images that pull their own
    # weights leave this unset.
    model_data_url = trimspace(var.model_data_url) != "" ? var.model_data_url : null
  }

  tags = merge(local.tags, { Name = "${var.project}-model" })

  lifecycle {
    precondition {
      # model_image must be non-empty when inference is on — SageMaker can't
      # host a model without a servable container. Enforced here (not as a
      # var validation) because the requirement is conditional on
      # var.enable_inference, and TF forbids cross-variable conditions in a
      # variable validation block.
      condition     = trimspace(var.model_image) != ""
      error_message = "model_image must be a non-empty ECR/SageMaker container image URI when enable_inference is true — SageMaker cannot host a model without a servable container."
    }
  }
}

resource "aws_sagemaker_endpoint_configuration" "inference" {
  count = local.enable_inference ? 1 : 0

  name = "${var.project}-endpoint-config"

  production_variants {
    variant_name           = "primary"
    model_name             = aws_sagemaker_model.inference[0].name
    initial_instance_count = 1
    instance_type          = var.endpoint_instance_type
    initial_variant_weight = 1.0
  }

  tags = merge(local.tags, { Name = "${var.project}-endpoint-config" })
}

# Endpoint create blocks until the production variant reaches InService —
# pulling the image + (optionally) the model artifact and warming the container
# can take several minutes, longer for large GPU LLM images. The aws provider
# 6.x aws_sagemaker_endpoint resource does NOT expose a configurable `timeouts`
# block (verified against `terraform providers schema -json`); it uses the
# provider's built-in InService wait. A slow-but-healthy rollout is therefore
# bounded by the provider default, not a knob we can set here.
resource "aws_sagemaker_endpoint" "inference" {
  count = local.enable_inference ? 1 : 0

  name                 = "${var.project}-endpoint"
  endpoint_config_name = aws_sagemaker_endpoint_configuration.inference[0].name

  tags = merge(local.tags, { Name = "${var.project}-endpoint" })
}

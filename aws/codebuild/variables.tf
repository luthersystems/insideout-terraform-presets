variable "project" {
  description = "Naming / Project-tag prefix for stack resources. The InsideOut inspector filters AWS resources by exact `Project = <project>` match — this value also seeds the CodeBuild project name (`<project>-<codebuild_project_name>`), the IAM role name (`<project>-codebuild`), and the optional logs bucket name. Capped at 40 chars so the longest derived name (`<project>-codebuild-logs-<6hex>`) stays inside AWS's 63-char S3 bucket / 64-char IAM identifier limits."
  type        = string
  default     = "demo"

  validation {
    condition     = length(trimspace(var.project)) > 0
    error_message = "project must be a non-empty string."
  }

  validation {
    condition     = length(var.project) <= 40
    error_message = "project must be 40 chars or fewer so derived CodeBuild project / IAM role / S3 bucket names fit inside AWS's 63/64-char identifier limits."
  }
}

variable "region" {
  description = "AWS region. Passed into the luthername module so the standard tag set carries the region and into the inline IAM policy ARNs (logs:CreateLogGroup resource scope, ec2:CreateNetworkInterfacePermission subnet ARNs). The AWS provider itself picks the region up from provider config."
  type        = string
  default     = "us-east-1"
}

variable "environment" {
  description = "Deployment environment (e.g. production, staging, sandbox). Feeds the luthername module's standard tag set; not used elsewhere in the preset."
  type        = string
  default     = "sandbox"

  validation {
    condition     = length(trimspace(var.environment)) > 0
    error_message = "environment must be a non-empty string."
  }
}

variable "tags" {
  description = "Extra tags merged onto every taggable resource. The preset always sets the standard luthername tag set (including `Project = var.project`); entries here override or extend that base set."
  type        = map(string)
  default     = {}
}

# -----------------------------------------------------------------------------
# Project shape
# -----------------------------------------------------------------------------

variable "codebuild_project_name" {
  description = "CodeBuild project name component. The composed project name is `<project>-<codebuild_project_name>` (matches the gcp/cloud_build pattern of project-prefixed naming for inspector attribution)."
  type        = string
  default     = "build"

  validation {
    condition     = can(regex("^[A-Za-z][A-Za-z0-9_-]{1,38}[A-Za-z0-9]$", var.codebuild_project_name))
    error_message = "codebuild_project_name must be 3-40 chars, start with a letter, end alphanumeric, contain only letters/digits/underscores/hyphens (CodeBuild naming rule)."
  }
}

variable "build_image" {
  description = "Container image the build runs inside. Defaults to AWS's standard managed image (`aws/codebuild/standard:7.0`, Ubuntu 22.04 with the standard runtime matrix preinstalled). For private ECR images use `<account>.dkr.ecr.<region>.amazonaws.com/<repo>:<tag>` — the preset's service role grants ECR pull permissions."
  type        = string
  default     = "aws/codebuild/standard:7.0"

  validation {
    condition     = length(trimspace(var.build_image)) > 0
    error_message = "build_image must be a non-empty string."
  }
}

variable "compute_type" {
  description = "CodeBuild compute class. Determines vCPU / memory / disk for each build container. AWS accepts BUILD_GENERAL1_SMALL (3 GB RAM, 2 vCPU), MEDIUM (7 GB / 4 vCPU), LARGE (15 GB / 8 vCPU), or 2XLARGE (145 GB / 72 vCPU). https://docs.aws.amazon.com/codebuild/latest/userguide/build-env-ref-compute-types.html"
  type        = string
  default     = "BUILD_GENERAL1_SMALL"

  validation {
    condition     = contains(["BUILD_GENERAL1_SMALL", "BUILD_GENERAL1_MEDIUM", "BUILD_GENERAL1_LARGE", "BUILD_GENERAL1_2XLARGE"], var.compute_type)
    error_message = "compute_type must be one of: BUILD_GENERAL1_SMALL, BUILD_GENERAL1_MEDIUM, BUILD_GENERAL1_LARGE, BUILD_GENERAL1_2XLARGE."
  }
}

# -----------------------------------------------------------------------------
# Source
# -----------------------------------------------------------------------------

variable "source_type" {
  description = "CodeBuild source provider. CODECOMMIT pulls from AWS CodeCommit, GITHUB from a public/private GitHub repo (private repos need an `aws_codebuild_source_credential` set up out-of-stack), S3 from a versioned object, and NO_SOURCE runs a buildspec-only build with no checkout. https://docs.aws.amazon.com/codebuild/latest/APIReference/API_ProjectSource.html"
  type        = string
  default     = "GITHUB"

  validation {
    condition     = contains(["CODECOMMIT", "GITHUB", "S3", "NO_SOURCE"], var.source_type)
    error_message = "source_type must be one of: CODECOMMIT, GITHUB, S3, NO_SOURCE."
  }
}

variable "source_location" {
  description = "Source location for `source_type`. For GITHUB the HTTPS clone URL (e.g. `https://github.com/owner/repo.git`); for CODECOMMIT the repository HTTPS URL; for S3 the `<bucket>/<key>` path to a zipped source archive. Ignored (and the preset sets it to null) when source_type = NO_SOURCE — provide an inline `buildspec` instead. Empty default keeps the variable optional so a NO_SOURCE single-module preview composes cleanly; CodeBuild's own apply-time validator rejects an empty location for any other source_type."
  type        = string
  default     = ""
}

variable "buildspec" {
  description = "Inline buildspec.yml contents OR a relative path to a buildspec file inside the source. Null defers to a `buildspec.yml` at the source root (CodeBuild's default behaviour). For NO_SOURCE builds an inline string is the only option since there's no source tree to read from."
  type        = string
  default     = null
}

# -----------------------------------------------------------------------------
# Artifacts
# -----------------------------------------------------------------------------

variable "artifacts_type" {
  description = "Where CodeBuild publishes build artifacts. NO_ARTIFACTS skips publication (the default for test-only or deploy-via-pipeline builds), S3 uploads to a bucket the caller owns, and CODEPIPELINE hands the artifact back to an upstream CodePipeline stage. https://docs.aws.amazon.com/codebuild/latest/APIReference/API_ProjectArtifacts.html"
  type        = string
  default     = "NO_ARTIFACTS"

  validation {
    condition     = contains(["NO_ARTIFACTS", "S3", "CODEPIPELINE"], var.artifacts_type)
    error_message = "artifacts_type must be one of: NO_ARTIFACTS, S3, CODEPIPELINE."
  }
}

variable "artifacts_location" {
  description = "Bucket name (without leading s3:// or trailing slash) when `artifacts_type = S3`. Ignored otherwise (the preset sets it to null for NO_ARTIFACTS / CODEPIPELINE). The caller owns the bucket — the preset does NOT provision it. Empty default keeps the variable optional for the NO_ARTIFACTS / CODEPIPELINE common cases; CodeBuild's own apply-time validator rejects an empty location when artifacts_type = S3."
  type        = string
  default     = ""
}

# -----------------------------------------------------------------------------
# Optional S3 logs bucket
# -----------------------------------------------------------------------------

variable "enable_s3_logs" {
  description = "Whether to provision a project-scoped S3 bucket for build logs in addition to the always-on CloudWatch Logs stream. When true, the preset creates the bucket with versioning + AES256 encryption + public-access block, grants the service role read/write to it, and wires `logs_config.s3_logs.location` to `<bucket>/build-logs/`."
  type        = bool
  default     = false
}

# -----------------------------------------------------------------------------
# Optional VPC config
#
# vpc_id + subnet_ids are normally wired by the composer's DefaultWiring
# from module.aws_vpc when KeyAWSVPC is selected. They're consumed only
# when subnet_ids is non-empty (the preset gates the vpc_config block on
# `length(var.subnet_ids) > 0`), so leaving them empty on a
# public-network build is fine. security_group_ids is caller-supplied —
# the preset doesn't create one; callers can reuse a VPC SG or
# provision one out-of-stack.
# -----------------------------------------------------------------------------

variable "vpc_id" {
  description = "VPC ID for the CodeBuild project's VPC config. Only consulted when subnet_ids is non-empty (the preset gates the vpc_config block on subnet_ids length). Composer wires this from `module.aws_vpc.vpc_id` automatically when KeyAWSVPC is selected (which is the ImplicitDependencies default for KeyAWSCodeBuild)."
  type        = string
  default     = ""
}

variable "subnet_ids" {
  description = "Subnet IDs the CodeBuild project's build ENIs land in. Non-empty turns the project's vpc_config block on (private builds with access to RDS / ElastiCache / internal endpoints); empty leaves it off (builds run in CodeBuild's public network plane). Use private subnets for actual private build access. Composer wires this from `module.aws_vpc.private_subnet_ids` automatically when KeyAWSVPC is selected."
  type        = list(string)
  default     = []
}

variable "security_group_ids" {
  description = "Security group IDs attached to the build ENIs. Required by AWS provider when subnet_ids is non-empty (the vpc_config block needs at least one SG). The preset does NOT provision an SG — callers reuse a VPC default SG or create one out-of-stack for egress rules."
  type        = list(string)
  default     = []
}

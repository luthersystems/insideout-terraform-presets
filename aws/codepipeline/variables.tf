variable "project" {
  description = "Project name. Prefixes every resource name and sets the Project tag."
  type        = string
}

variable "region" {
  description = "AWS region. Passed to the luthername module for the standard tag set."
  type        = string
  default     = "us-east-1"
}

variable "environment" {
  description = "Environment (dev, staging, prod)."
  type        = string
  default     = "dev"
}

variable "tags" {
  description = "Extra tags merged onto every taggable resource."
  type        = map(string)
  default     = {}
}

variable "pipeline_name" {
  description = "Short pipeline name. The pipeline is `<project>-<pipeline_name>`."
  type        = string
  default     = "pipeline"

  validation {
    condition     = can(regex("^[A-Za-z0-9.@_-]{1,60}$", var.pipeline_name))
    error_message = "pipeline_name must be 1 to 60 of A-Z a-z 0-9 . @ _ -."
  }
}

variable "source_type" {
  description = "Source of the pipeline: GITHUB (through a CodeStar connection) or S3 (a versioned object)."
  type        = string
  default     = "GITHUB"

  validation {
    condition     = contains(["GITHUB", "S3"], var.source_type)
    error_message = "source_type must be GITHUB or S3."
  }
}

variable "github_repository" {
  description = "GitHub repository in OWNER/REPO form, for source_type GITHUB."
  type        = string
  default     = ""
}

variable "branch" {
  description = "Branch the pipeline builds, for source_type GITHUB."
  type        = string
  default     = "main"

  validation {
    condition     = trimspace(var.branch) != ""
    error_message = "branch must not be empty."
  }
}

variable "connection_arn" {
  description = "ARN of an existing CodeStar connection to GitHub, already approved. Empty (the default) creates a new connection, which a person must approve once in the AWS console."
  type        = string
  default     = ""

  validation {
    condition     = var.connection_arn == "" || can(regex("^arn:aws[a-z-]*:(codestar-connections|codeconnections):[a-z0-9-]+:[0-9]{12}:connection/", var.connection_arn))
    error_message = "connection_arn must be a CodeStar connection ARN."
  }
}

variable "source_s3_bucket" {
  description = "Bucket of the source object, for source_type S3. The bucket must have versioning on."
  type        = string
  default     = ""
}

variable "source_s3_key" {
  description = "Key of the source object (a zip), for source_type S3."
  type        = string
  default     = ""
}

variable "detect_changes" {
  description = "Start the pipeline on each push to the branch (GITHUB) or each new object version (S3)."
  type        = bool
  default     = true
}

variable "codebuild_project_name" {
  description = "Name of the CodeBuild project the Build stage runs. The composer wires it from module.aws_codebuild."
  type        = string
}

variable "codebuild_project_arn" {
  description = "ARN of that CodeBuild project, for the service role policy. The composer wires it from module.aws_codebuild."
  type        = string
}

variable "codebuild_service_role_name" {
  description = "Name of that CodeBuild project's service role. The preset gives it access to the artifact bucket. The composer wires it from module.aws_codebuild."
  type        = string
}

variable "kms_key_arn" {
  description = "ARN of a customer-managed KMS key for the artifact bucket and the artifact store. Null (default) keeps SSE-S3 (AES256). A key ARN, not an alias: the roles' policies name it."
  type        = string
  default     = null
  validation {
    condition     = var.kms_key_arn == null ? true : can(regex("^arn:aws[a-z-]*:kms:[a-z0-9-]+:[0-9]{12}:key/.+$", var.kms_key_arn))
    error_message = "kms_key_arn must be a KMS key ARN (arn:aws:kms:<region>:<account>:key/<id>)."
  }
}

variable "iam_scope" {
  description = "IAM path and permissions boundary for the roles, customer managed policies and instance profiles this module creates. The scoped deploy policy allows IAM writes only under /insideout/<project>/ and creates roles only with the workload boundary (sandbox-infrastructure-template#147). Both are set together, because the boundary passes only roles on the path. null keeps the module's defaults. Changing it replaces the roles."
  type = object({
    path                 = string
    permissions_boundary = string
  })
  default = null

  validation {
    condition = var.iam_scope == null ? true : (
      can(regex("^/insideout/[A-Za-z0-9+=,.@_-]+/$", var.iam_scope.path)) &&
      can(regex("^arn:aws[a-z-]*:iam::([0-9]{12}|aws):policy/", var.iam_scope.permissions_boundary))
    )
    error_message = "iam_scope.path must be /insideout/<project>/ and iam_scope.permissions_boundary an IAM policy ARN."
  }
}

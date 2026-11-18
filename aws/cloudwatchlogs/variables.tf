variable "region" {
  description = "AWS region"
  type        = string
  default     = "us-east-1"

  validation {
    condition     = length(trimspace(var.region)) > 0
    error_message = "region must be a non-empty string (e.g., us-west-2)."
  }
}

variable "project" {
  description = "Project/prefix for naming"
  type        = string
  default     = "demo"

  validation {
    condition     = length(trimspace(var.project)) > 0
    error_message = "project must be a non-empty string."
  }
}

variable "environment" {
  description = "Deployment environment (e.g. production, staging, sandbox)"
  type        = string
  validation {
    condition     = length(trimspace(var.environment)) > 0
    error_message = "environment must be a non-empty string."
  }
}

variable "retention_in_days" {
  description = "CloudWatch Logs retention"
  type        = number
  default     = 30

  validation {
    condition     = var.retention_in_days >= 1
    error_message = "retention_in_days must be >= 1."
  }
}

variable "kms_key_arn" {
  description = "Optional KMS key ARN for log group encryption (leave empty to use AWS-managed)"
  type        = string
  default     = ""

  # Allow empty string, otherwise require a KMS key ARN-ish shape.
  validation {
    condition     = var.kms_key_arn == "" || can(regex("^arn:aws(-[a-z0-9]+)?:kms:[a-z0-9-]+:\\d{12}:key\\/.+$", var.kms_key_arn))
    error_message = "kms_key_arn must be empty or a valid KMS key ARN."
  }
}

variable "writer_principals" {
  description = "AWS service principals allowed to assume the writer role (e.g., \"ec2.amazonaws.com\")"
  type        = list(string)
  default     = ["ec2.amazonaws.com"]

  # Ensure each element is a non-empty string; allow empty list if you prefer to manage the role differently.
  validation {
    condition     = alltrue([for p in var.writer_principals : length(trimspace(p)) > 0])
    error_message = "writer_principals entries must be non-empty strings (e.g., \"ec2.amazonaws.com\")."
  }
}

variable "tags" {
  description = "Extra resource tags"
  type        = map(string)
  default     = {}
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

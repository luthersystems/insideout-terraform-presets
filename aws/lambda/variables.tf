variable "region" {
  description = "AWS region"
  type        = string
  default     = "us-east-1"
}

variable "project" {
  description = "Name/prefix for resources"
  type        = string
  default     = "demo"
}

variable "environment" {
  description = "Deployment environment (e.g. production, staging, sandbox)"
  type        = string
  validation {
    condition     = length(trimspace(var.environment)) > 0
    error_message = "environment must be a non-empty string."
  }
}

variable "runtime" {
  description = "Lambda runtime (e.g., nodejs20.x, python3.12, go1.x, java21)"
  type        = string
  default     = "nodejs20.x"
  validation {
    condition     = length(trimspace(var.runtime)) > 0 && can(regex("^[A-Za-z0-9][A-Za-z0-9_.-]*$", var.runtime))
    error_message = "runtime must be a non-empty Lambda runtime token (e.g., nodejs20.x, python3.12, go1.x, java21)."
  }
}

variable "memory_size" {
  description = "Memory size in MB"
  type        = number
  default     = 128
  validation {
    condition     = var.memory_size >= 128 && var.memory_size <= 10240
    error_message = "memory_size must be between 128 and 10240 MB."
  }
}

variable "timeout" {
  description = "Timeout in seconds"
  type        = number
  default     = 3
  validation {
    condition     = var.timeout >= 1 && var.timeout <= 900
    error_message = "timeout must be between 1 and 900 seconds."
  }
}

variable "handler" {
  description = "Lambda handler (e.g., index.handler)"
  type        = string
  default     = "index.handler"
}

variable "enable_vpc" {
  description = "Enable VPC access for Lambda (must be true when vpc_id is set)"
  type        = bool
  default     = false
}

variable "vpc_id" {
  description = "VPC ID for Lambda VPC access (required when enable_vpc is true)"
  type        = string
  default     = null
  validation {
    condition     = var.vpc_id == null ? true : length(trimspace(var.vpc_id)) > 0
    error_message = "vpc_id must be a non-empty string when provided."
  }
}

variable "subnet_ids" {
  description = "Subnet IDs for VPC access (required when enable_vpc is true)"
  type        = list(string)
  default     = []
}

variable "security_group_ids" {
  description = "Security Group IDs for VPC access. When empty and enable_vpc is true, a default security group with egress-all is created automatically."
  type        = list(string)
  default     = []
}

variable "environment_variables" {
  description = "Environment variables for Lambda"
  type        = map(string)
  default     = {}
}

variable "log_retention_days" {
  description = "CloudWatch log retention in days"
  type        = number
  default     = 14
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

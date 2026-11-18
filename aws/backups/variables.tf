variable "region" {
  description = "AWS region"
  type        = string
  default     = "us-east-1"
  validation {
    condition     = length(trimspace(var.region)) > 0
    error_message = "region must be a non-empty string."
  }
}

variable "project" {
  description = "Name/prefix for resources"
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

# Toggle which service selections/rules to include
variable "enable_ec2_ebs" {
  description = "Enable EC2/EBS backup plan/selection"
  type        = bool
  default     = false
}

variable "enable_rds" {
  description = "Enable RDS backup plan/selection"
  type        = bool
  default     = false
}

variable "enable_dynamodb" {
  description = "Enable DynamoDB backup plan/selection"
  type        = bool
  default     = false
}

variable "enable_s3" {
  description = "Enable S3 backup plan/selection"
  type        = bool
  default     = false
}

# Shared defaults (keep loose typing to avoid root type conflicts)
# Expected keys (all optional):
#   schedule_expression, schedule_expression_timezone, start_window, completion_window,
#   enable_continuous_backup, retention_days, cold_storage_after_days, recovery_point_tags (map)
variable "default_rule" {
  description = "Default rule settings applied to enabled services (loose shape)"
  type        = any
  default     = {}
  validation {
    condition     = var.default_rule == null || can(jsonencode(var.default_rule))
    error_message = "default_rule must be an object-like value (map)."
  }
}

# Per-service overrides. Shape mirrors default_rule plus:
#   selection = {
#     resource_arns  = list(string)   # explicit ARNs
#     selection_tags = list(object({ type = string, key = string, value = string }))
#   }
variable "ec2_ebs_rule" {
  description = "EC2/EBS rule override (loose shape)"
  type        = any
  default     = {}
  validation {
    condition     = var.ec2_ebs_rule == null || can(jsonencode(var.ec2_ebs_rule))
    error_message = "ec2_ebs_rule must be an object-like value (map)."
  }
}

variable "rds_rule" {
  description = "RDS rule override (loose shape)"
  type        = any
  default     = {}
  validation {
    condition     = var.rds_rule == null || can(jsonencode(var.rds_rule))
    error_message = "rds_rule must be an object-like value (map)."
  }
}

variable "dynamodb_rule" {
  description = "DynamoDB rule override (loose shape)"
  type        = any
  default     = {}
  validation {
    condition     = var.dynamodb_rule == null || can(jsonencode(var.dynamodb_rule))
    error_message = "dynamodb_rule must be an object-like value (map)."
  }
}

variable "s3_rule" {
  description = "S3 rule override (loose shape)"
  type        = any
  default     = {}
  validation {
    condition     = var.s3_rule == null || can(jsonencode(var.s3_rule))
    error_message = "s3_rule must be an object-like value (map)."
  }
}

variable "tags" {
  description = "Common resource tags"
  type        = map(string)
  default     = {}
}

variable "iam_path" {
  description = "IAM path for the roles, customer managed policies and instance profiles this module creates. The scoped deploy policy allows IAM writes only under /insideout/<project>/ (sandbox-infrastructure-template#147). null keeps the module's default path. Changing it replaces the roles."
  type        = string
  default     = null

  validation {
    condition     = var.iam_path == null ? true : can(regex("^/([A-Za-z0-9+=,.@_-]+/)*$", var.iam_path))
    error_message = "iam_path must start and end with / and hold only IAM path characters."
  }
}

variable "iam_permissions_boundary" {
  description = "ARN of the permissions boundary for every role this module creates. The scoped deploy policy refuses a role without the workload boundary (sandbox-infrastructure-template#147). null sets no boundary."
  type        = string
  default     = null

  validation {
    condition     = var.iam_permissions_boundary == null ? true : can(regex("^arn:aws[a-z-]*:iam::([0-9]{12}|aws):policy/", var.iam_permissions_boundary))
    error_message = "iam_permissions_boundary must be an IAM policy ARN."
  }
}

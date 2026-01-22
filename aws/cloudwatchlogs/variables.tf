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

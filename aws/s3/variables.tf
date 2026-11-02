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
  description = "Project/prefix used in bucket name (lowercase letters/numbers/dots/dashes)"
  type        = string
  default     = "demo"
  validation {
    condition     = length(trimspace(var.project)) > 0 && can(regex("^[a-z0-9.-]+$", var.project))
    error_message = "project must be lowercase and only contain letters, numbers, dots, or dashes."
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

variable "versioning" {
  description = "Enable S3 bucket versioning"
  type        = bool
  default     = false
}

variable "force_destroy" {
  description = "Allow bucket to be destroyed even if it contains objects"
  type        = bool
  default     = false
}

variable "enable_lifecycle" {
  description = "Create demo lifecycle rules (IA @30d, Glacier @90d)"
  type        = bool
  default     = false
}

variable "kms_key_arn" {
  description = "ARN of a customer-managed KMS key for SSE-KMS on the bucket. Null (default) keeps SSE-S3 (AES256). When set, every reader of the bucket (for example CloudFront origin access control or an app role) also needs kms:Decrypt on the key, which the caller grants in the key policy."
  type        = string
  default     = null
  validation {
    condition     = var.kms_key_arn == null ? true : can(regex("^arn:aws[a-z-]*:kms:[a-z0-9-]+:[0-9]{12}:(key|alias)/.+$", var.kms_key_arn))
    error_message = "kms_key_arn must be a KMS key or alias ARN (arn:aws:kms:<region>:<account>:key/<id>)."
  }
}

variable "tags" {
  description = "Extra tags"
  type        = map(string)
  default     = {}
}

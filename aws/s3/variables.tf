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

variable "tags" {
  description = "Extra tags"
  type        = map(string)
  default     = {}
}

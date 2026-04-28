variable "project" {
  description = "Naming/label prefix for stack resources (NOT a GCP project ID — see var.project_id)."
  type        = string

  validation {
    condition     = length(trimspace(var.project)) > 0
    error_message = "project must be a non-empty string."
  }
}

variable "project_id" {
  description = "Real GCP project ID where resources are created (e.g. \"my-prod-12345\"). Distinct from var.project, which is the naming/label prefix and need not be a valid GCP project ID."
  type        = string

  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{4,28}[a-z0-9]$", var.project_id))
    error_message = "project_id must be a valid GCP project ID: 6–30 chars, lowercase letters/digits/hyphens, start with a letter, end alphanumeric."
  }
}

variable "region" {
  description = "GCP region"
  type        = string
  default     = "us-central1"
}

variable "function_name" {
  description = "Name of the Cloud Function"
  type        = string
  default     = "function"
}

variable "runtime" {
  description = "Runtime environment (e.g., nodejs20, python312, go122)"
  type        = string
  default     = "nodejs20"

  validation {
    condition     = length(trimspace(var.runtime)) > 0
    error_message = "runtime must be a non-empty string."
  }
}

variable "entry_point" {
  description = "Function entry point name"
  type        = string
  default     = "helloWorld"
}

variable "available_memory_mb" {
  description = "Memory available to the function in MB"
  type        = number
  default     = 256

  validation {
    condition     = var.available_memory_mb >= 128
    error_message = "available_memory_mb must be at least 128."
  }
}

variable "timeout_seconds" {
  description = "Function timeout in seconds"
  type        = number
  default     = 60

  validation {
    condition     = var.timeout_seconds >= 1 && var.timeout_seconds <= 3600
    error_message = "timeout_seconds must be between 1 and 3600."
  }
}

variable "max_instances" {
  description = "Maximum number of concurrent instances"
  type        = number
  default     = 100
}

variable "min_instances" {
  description = "Minimum number of instances (0 for scale-to-zero)"
  type        = number
  default     = 0
}

variable "vpc_connector" {
  description = "VPC Access Connector ID for private networking (optional)"
  type        = string
  default     = ""
}

variable "vpc_egress" {
  description = "VPC egress setting"
  type        = string
  default     = "PRIVATE_RANGES_ONLY"

  validation {
    condition     = contains(["ALL_TRAFFIC", "PRIVATE_RANGES_ONLY"], var.vpc_egress)
    error_message = "vpc_egress must be one of: ALL_TRAFFIC, PRIVATE_RANGES_ONLY."
  }
}

variable "env_vars" {
  description = "Environment variables for the function"
  type        = map(string)
  default     = {}
}

variable "source_archive_bucket" {
  description = "GCS bucket for function source code (auto-created if empty)"
  type        = string
  default     = ""
}

variable "source_archive_object" {
  description = "GCS object path for function source archive (uses placeholder if empty)"
  type        = string
  default     = ""
}

variable "allow_unauthenticated" {
  description = "Allow unauthenticated invocations (public)"
  type        = bool
  default     = true
}

variable "labels" {
  description = "Labels to apply to resources"
  type        = map(string)
  default     = {}
}

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

variable "region" { type = string }

variable "retention_days" {
  description = "Number of days to retain archived log objects in the logs bucket before deletion."
  type        = number
  default     = 30

  validation {
    condition     = var.retention_days >= 1
    error_message = "retention_days must be >= 1."
  }
}

variable "labels" {
  description = "Additional labels to apply to module resources. Merged with module-managed labels."
  type        = map(string)
  default     = {}
}

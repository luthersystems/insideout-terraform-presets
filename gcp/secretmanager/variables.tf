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
  description = "GCP region (not used, secrets are global)"
  type        = string
  default     = "us-central1"
}

variable "secrets" {
  description = "List of secrets to create"
  type = list(object({
    name   = string
    value  = optional(string)
    labels = optional(map(string), {})
    replication = optional(object({
      automatic = optional(bool, true)
      user_managed = optional(list(object({
        location     = string
        kms_key_name = optional(string)
      })))
    }))
  }))
  default = []
}

variable "iam_bindings" {
  description = "IAM bindings for secrets"
  type = list(object({
    secret_name = string
    role        = string
    members     = list(string)
  }))
  default = []
}

variable "labels" {
  description = "Labels to apply to all secrets"
  type        = map(string)
  default     = {}
}


variable "project" {
  description = "GCP project ID"
  type        = string

  validation {
    condition     = length(trimspace(var.project)) > 0
    error_message = "project must be a non-empty string."
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


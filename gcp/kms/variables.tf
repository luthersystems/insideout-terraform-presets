variable "project" {
  description = "GCP project ID"
  type        = string

  validation {
    condition     = length(trimspace(var.project)) > 0
    error_message = "project must be a non-empty string."
  }
}

variable "region" {
  description = "GCP region for the key ring"
  type        = string
  default     = "us-central1"
}

variable "keyring_name" {
  description = "Name of the key ring"
  type        = string
  default     = "main"
}

variable "keys" {
  description = "List of keys to create"
  type = list(object({
    name             = string
    purpose          = optional(string, "ENCRYPT_DECRYPT")
    rotation_period  = optional(string, "7776000s") # 90 days
    algorithm        = optional(string, "GOOGLE_SYMMETRIC_ENCRYPTION")
    protection_level = optional(string, "SOFTWARE")
    labels           = optional(map(string), {})
  }))
  default = [{
    name = "default"
  }]
}

variable "prevent_destroy" {
  description = "Prevent key destruction"
  type        = bool
  default     = true
}

variable "iam_bindings" {
  description = "IAM bindings for keys"
  type = list(object({
    key_name = string
    role     = string
    members  = list(string)
  }))
  default = []
}

variable "labels" {
  description = "Labels to apply to the key ring"
  type        = map(string)
  default     = {}
}


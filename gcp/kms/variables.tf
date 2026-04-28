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

  validation {
    condition     = length(var.keys) > 0
    error_message = "var.keys must contain at least one entry."
  }
  validation {
    condition     = length(distinct([for k in var.keys : k.rotation_period])) <= 1
    error_message = "All keys must share the same rotation_period (the upstream KMS module applies one value to every key in the keyring)."
  }
  validation {
    condition     = length(distinct([for k in var.keys : k.algorithm])) <= 1
    error_message = "All keys must share the same algorithm."
  }
  validation {
    condition     = length(distinct([for k in var.keys : k.protection_level])) <= 1
    error_message = "All keys must share the same protection_level."
  }
  validation {
    condition     = length(distinct([for k in var.keys : k.purpose])) <= 1
    error_message = "All keys must share the same purpose."
  }
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


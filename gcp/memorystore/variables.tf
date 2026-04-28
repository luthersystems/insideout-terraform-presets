variable "project" {
  description = "Naming/label prefix for stack resources (NOT a GCP project ID — see var.project_id)."
  type        = string
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

variable "name" {
  description = "Name of the Memorystore instance"
  type        = string
  default     = "redis"
}

variable "tier" {
  description = "Tier of the Memorystore instance (BASIC or STANDARD_HA)"
  type        = string
  default     = "BASIC"
  validation {
    condition     = contains(["BASIC", "STANDARD_HA"], var.tier)
    error_message = "tier must be one of: BASIC, STANDARD_HA."
  }
}

variable "memory_size_gb" {
  description = "Memory size in GB"
  type        = number
  default     = 1
  validation {
    condition     = var.memory_size_gb >= 1
    error_message = "memory_size_gb must be >= 1."
  }
}

variable "authorized_network" {
  description = "VPC network self-link to attach to"
  type        = string
}

variable "redis_version" {
  description = "Redis version"
  type        = string
  default     = "REDIS_7_0"
}

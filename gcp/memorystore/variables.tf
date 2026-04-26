variable "project" {
  description = "GCP project ID"
  type        = string
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

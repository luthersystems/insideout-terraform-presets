variable "project" {
  description = "GCP project ID"
  type        = string

  validation {
    condition     = length(trimspace(var.project)) > 0
    error_message = "project must be a non-empty string."
  }
}

variable "region" {
  description = "GCP region (for regional buckets)"
  type        = string
  default     = "us-central1"
}

variable "bucket_name" {
  description = "Name of the bucket (must be globally unique)"
  type        = string
}

variable "location" {
  description = "Location for the bucket (region, dual-region, or multi-region)"
  type        = string
  default     = "US"
}

variable "storage_class" {
  description = "Storage class (STANDARD, NEARLINE, COLDLINE, ARCHIVE)"
  type        = string
  default     = "STANDARD"

  validation {
    condition     = contains(["STANDARD", "NEARLINE", "COLDLINE", "ARCHIVE"], var.storage_class)
    error_message = "storage_class must be one of: STANDARD, NEARLINE, COLDLINE, ARCHIVE."
  }
}

variable "uniform_bucket_level_access" {
  description = "Enable uniform bucket-level access (recommended)"
  type        = bool
  default     = true
}

variable "public_access_prevention" {
  description = "Public access prevention (enforced or inherited)"
  type        = string
  default     = "enforced"

  validation {
    condition     = contains(["enforced", "inherited"], var.public_access_prevention)
    error_message = "public_access_prevention must be 'enforced' or 'inherited'."
  }
}

variable "versioning_enabled" {
  description = "Enable object versioning"
  type        = bool
  default     = true
}

variable "lifecycle_rules" {
  description = "Lifecycle rules for automatic object management"
  type = list(object({
    action = object({
      type          = string
      storage_class = optional(string)
    })
    condition = object({
      age                        = optional(number)
      created_before             = optional(string)
      with_state                 = optional(string)
      matches_storage_class      = optional(list(string))
      num_newer_versions         = optional(number)
      custom_time_before         = optional(string)
      days_since_custom_time     = optional(number)
      days_since_noncurrent_time = optional(number)
      noncurrent_time_before     = optional(string)
    })
  }))
  default = []
}

variable "retention_policy" {
  description = "Retention policy for the bucket"
  type = object({
    retention_period = number
    is_locked        = optional(bool, false)
  })
  default = null
}

variable "cors" {
  description = "CORS configuration"
  type = list(object({
    origin          = list(string)
    method          = list(string)
    response_header = list(string)
    max_age_seconds = number
  }))
  default = []
}

variable "website" {
  description = "Static website configuration"
  type = object({
    main_page_suffix = string
    not_found_page   = string
  })
  default = null
}

variable "encryption_key" {
  description = "Cloud KMS key for encryption (uses Google-managed key if empty)"
  type        = string
  default     = null
}

variable "logging" {
  description = "Access logging configuration"
  type = object({
    log_bucket        = string
    log_object_prefix = optional(string)
  })
  default = null
}

variable "force_destroy" {
  description = "Allow deletion of bucket with objects"
  type        = bool
  default     = false
}

variable "labels" {
  description = "Labels to apply"
  type        = map(string)
  default     = {}
}


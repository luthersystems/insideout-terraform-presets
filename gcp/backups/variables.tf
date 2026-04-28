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
  description = "GCP region for backups"
  type        = string
  default     = "us-central1"
}

variable "enable_gcs_backups" {
  description = "Create a GCS bucket for backups"
  type        = bool
  default     = true
}

variable "enable_compute_snapshots" {
  description = "Create a snapshot schedule for Compute Engine disks"
  type        = bool
  default     = true
}

variable "backup_retention_days" {
  description = "Number of days to retain backups"
  type        = number
  default     = 30
}

variable "snapshot_retention_days" {
  description = "Number of days to retain disk snapshots"
  type        = number
  default     = 14
}

variable "snapshot_start_time" {
  description = "Time to start daily snapshots (HH:MM in UTC)"
  type        = string
  default     = "03:00"
}

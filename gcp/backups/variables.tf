variable "project" {
  description = "GCP project ID"
  type        = string
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

variable "project" {
  description = "GCP project ID"
  type        = string

  validation {
    condition     = length(trimspace(var.project)) > 0
    error_message = "project must be a non-empty string."
  }
}

variable "region" {
  description = "GCP region"
  type        = string
  default     = "us-central1"
}

variable "instance_name" {
  description = "Name of the Cloud SQL instance"
  type        = string
  default     = "main"
}

variable "database_version" {
  description = "Database version (POSTGRES_15, POSTGRES_14, MYSQL_8_0, etc.)"
  type        = string
  default     = "POSTGRES_15"
}

variable "tier" {
  description = "Machine tier (db-f1-micro, db-custom-2-7680, etc.)"
  type        = string
  default     = "db-custom-2-7680"
}

variable "disk_size_gb" {
  description = "Disk size in GB"
  type        = number
  default     = 20
}

variable "disk_type" {
  description = "Disk type (PD_SSD or PD_HDD)"
  type        = string
  default     = "PD_SSD"
}

variable "disk_autoresize" {
  description = "Enable disk auto-resize"
  type        = bool
  default     = true
}

variable "disk_autoresize_limit" {
  description = "Maximum disk size for auto-resize (0 = unlimited)"
  type        = number
  default     = 0
}

variable "availability_type" {
  description = "Availability type (REGIONAL for HA, ZONAL for single zone)"
  type        = string
  default     = "REGIONAL"

  validation {
    condition     = contains(["REGIONAL", "ZONAL"], var.availability_type)
    error_message = "availability_type must be REGIONAL or ZONAL."
  }
}

variable "network_self_link" {
  description = "VPC network self link for private IP"
  type        = string
  default     = null
}

variable "enable_private_ip" {
  description = "Enable private IP (requires network_self_link)"
  type        = bool
  default     = true
}

variable "enable_public_ip" {
  description = "Enable public IP"
  type        = bool
  default     = false
}

variable "authorized_networks" {
  description = "Networks authorized for public IP access"
  type = list(object({
    name  = string
    value = string
  }))
  default = []
}

variable "database_name" {
  description = "Name of the default database"
  type        = string
  default     = "main"
}

variable "user_name" {
  description = "Name of the default user"
  type        = string
  default     = "admin"
}

variable "user_password" {
  description = "Password for the default user (generated if empty)"
  type        = string
  default     = ""
  sensitive   = true
}

variable "backup_enabled" {
  description = "Enable automated backups"
  type        = bool
  default     = true
}

variable "backup_start_time" {
  description = "Start time for backup window (HH:MM format)"
  type        = string
  default     = "02:00"
}

variable "backup_location" {
  description = "Location for backups"
  type        = string
  default     = null
}

variable "point_in_time_recovery_enabled" {
  description = "Enable point-in-time recovery"
  type        = bool
  default     = true
}

variable "maintenance_window_day" {
  description = "Day of week for maintenance (1-7, Monday=1)"
  type        = number
  default     = 7
}

variable "maintenance_window_hour" {
  description = "Hour for maintenance window (0-23)"
  type        = number
  default     = 3
}

variable "deletion_protection" {
  description = "Prevent accidental deletion"
  type        = bool
  default     = true
}

variable "database_flags" {
  description = "Database flags"
  type = list(object({
    name  = string
    value = string
  }))
  default = []
}

variable "labels" {
  description = "Labels to apply"
  type        = map(string)
  default     = {}
}


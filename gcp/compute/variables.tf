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
  description = "GCP region"
  type        = string
  default     = "us-central1"
}

variable "zone" {
  description = "GCP zone"
  type        = string
  default     = "us-central1-a"
}

variable "instance_name" {
  description = "Name of the compute instance"
  type        = string
  default     = "main"
}

variable "machine_type" {
  description = "Machine type (e.g., e2-medium, n2-standard-2)"
  type        = string
  default     = "e2-medium"
}

variable "network_self_link" {
  description = "VPC network self link"
  type        = string
}

variable "subnet_self_link" {
  description = "Subnet self link"
  type        = string
}

variable "image_family" {
  description = "OS image family"
  type        = string
  default     = "ubuntu-2204-lts"
}

variable "image_project" {
  description = "Project containing the OS image"
  type        = string
  default     = "ubuntu-os-cloud"
}

variable "disk_size_gb" {
  description = "Boot disk size in GB"
  type        = number
  default     = 50
}

variable "disk_type" {
  description = "Boot disk type"
  type        = string
  default     = "pd-ssd"
}

variable "preemptible" {
  description = "Use preemptible (spot) instances"
  type        = bool
  default     = false
}

variable "enable_public_ip" {
  description = "Assign a public IP address"
  type        = bool
  default     = false
}

variable "service_account_email" {
  description = "Service account email (uses default compute SA if empty)"
  type        = string
  default     = ""
}

variable "service_account_scopes" {
  description = "OAuth scopes for the service account"
  type        = list(string)
  default     = ["cloud-platform"]
}

variable "tags" {
  description = "Network tags for firewall rules"
  type        = list(string)
  default     = []
}

variable "labels" {
  description = "Labels to apply to the instance"
  type        = map(string)
  default     = {}
}

variable "metadata" {
  description = "Metadata key/value pairs"
  type        = map(string)
  default     = {}
}

variable "startup_script" {
  description = "Startup script content"
  type        = string
  default     = ""
}


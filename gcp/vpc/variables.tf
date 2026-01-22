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

  validation {
    condition     = length(trimspace(var.region)) > 0
    error_message = "region must be a non-empty string (e.g., us-central1)."
  }
}

variable "network_name" {
  description = "Name of the VPC network"
  type        = string
  default     = "main"
}

variable "subnet_cidr" {
  description = "CIDR block for the primary subnet"
  type        = string
  default     = "10.1.0.0/16"

  validation {
    condition     = can(cidrnetmask(var.subnet_cidr))
    error_message = "subnet_cidr must be a valid IPv4 CIDR (e.g., 10.1.0.0/16)."
  }
}

variable "secondary_ranges" {
  description = "Secondary IP ranges for GKE pods/services"
  type = object({
    pods_cidr     = string
    services_cidr = string
  })
  default = {
    pods_cidr     = "10.2.0.0/16"
    services_cidr = "10.3.0.0/20"
  }
}

variable "enable_cloud_nat" {
  description = "Enable Cloud NAT for private instances"
  type        = bool
  default     = true
}

variable "gke_cluster_name" {
  description = "If set, create secondary ranges for this GKE cluster"
  type        = string
  default     = null

  validation {
    condition     = var.gke_cluster_name == null ? true : length(trimspace(var.gke_cluster_name)) > 0
    error_message = "gke_cluster_name, when provided, must be a non-empty string."
  }
}


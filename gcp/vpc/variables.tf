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

variable "enable_serverless_connector" {
  description = "Create a Serverless VPC Access Connector for Cloud Run / Cloud Functions"
  type        = bool
  default     = false
}

variable "connector_cidr" {
  description = "CIDR range for the Serverless VPC Access Connector (/28 required)"
  type        = string
  default     = "10.8.0.0/28"

  validation {
    condition     = can(cidrnetmask(var.connector_cidr))
    error_message = "connector_cidr must be a valid IPv4 CIDR (e.g., 10.8.0.0/28)."
  }
}

variable "connector_min_instances" {
  description = "Minimum instances for the Serverless VPC Access Connector. GCP requires >= 2."
  type        = number
  default     = 2

  validation {
    condition     = var.connector_min_instances >= 2
    error_message = "connector_min_instances must be >= 2 (GCP minimum)."
  }
}

variable "connector_max_instances" {
  description = "Maximum instances for the Serverless VPC Access Connector. GCP allows 3..10 and requires it (or max_throughput) to be set."
  type        = number
  default     = 3

  validation {
    condition     = var.connector_max_instances >= 3 && var.connector_max_instances <= 10
    error_message = "connector_max_instances must be between 3 and 10."
  }
}

# Private Services Access / servicenetworking peering (issue #774). A fully
# private Vertex AI index endpoint (#764/#600) requires the consumer VPC to
# have a servicenetworking.googleapis.com private connection: a reserved
# VPC_PEERING global address + a google_service_networking_connection. gcp/vpc
# does not provision this today, so #773's private-endpoint path needs the
# peering created out-of-band. This lets gcp/vpc own one shared peering range
# that the Vertex private endpoint (and private-IP CloudSQL/Memorystore) can
# consume. Off by default — the public-endpoint paths need none of it.
variable "enable_service_networking" {
  description = "Create a Private Services Access (servicenetworking) peering on this VPC: a reserved VPC_PEERING range + a google_service_networking_connection. Required for fully private Vertex AI index endpoints (#774/#600) and any managed service that consumes this VPC's peering for private IP. Default false (public paths need none of it)."
  type        = bool
  default     = false
}

variable "service_networking_prefix_length" {
  description = "Prefix length of the reserved VPC_PEERING range allocated for Private Services Access. GCP requires /8../30; /16 (the default) matches the gcp/cloudsql reservation."
  type        = number
  default     = 16

  validation {
    condition     = var.service_networking_prefix_length >= 8 && var.service_networking_prefix_length <= 30
    error_message = "service_networking_prefix_length must be between 8 and 30."
  }
}

variable "labels" {
  description = "Resource labels merged with the standard { project = var.project } identity label on label-capable resources (e.g. the Private Services Access range)."
  type        = map(string)
  default     = {}
}


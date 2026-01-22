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

variable "service_name" {
  description = "Name of the Cloud Run service"
  type        = string
  default     = "app"
}

variable "image" {
  description = "Container image to deploy (e.g., gcr.io/project/image:tag)"
  type        = string
  default     = "us-docker.pkg.dev/cloudrun/container/hello"
}

variable "memory" {
  description = "Memory allocation (e.g., 512Mi, 1Gi, 2Gi)"
  type        = string
  default     = "512Mi"
}

variable "cpu" {
  description = "CPU allocation (e.g., 1, 2, 4)"
  type        = string
  default     = "1"
}

variable "min_instances" {
  description = "Minimum number of instances (0 for scale-to-zero)"
  type        = number
  default     = 0
}

variable "max_instances" {
  description = "Maximum number of instances"
  type        = number
  default     = 100
}

variable "timeout_seconds" {
  description = "Request timeout in seconds"
  type        = number
  default     = 300
}

variable "concurrency" {
  description = "Maximum concurrent requests per instance"
  type        = number
  default     = 80
}

variable "port" {
  description = "Container port"
  type        = number
  default     = 8080
}

variable "env_vars" {
  description = "Environment variables for the container"
  type        = map(string)
  default     = {}
}

variable "vpc_connector" {
  description = "VPC Access Connector name for private networking (optional)"
  type        = string
  default     = ""
}

variable "vpc_egress" {
  description = "VPC egress setting: all-traffic or private-ranges-only"
  type        = string
  default     = "private-ranges-only"

  validation {
    condition     = contains(["all-traffic", "private-ranges-only"], var.vpc_egress)
    error_message = "vpc_egress must be one of: all-traffic, private-ranges-only."
  }
}

variable "allow_unauthenticated" {
  description = "Allow unauthenticated access (public)"
  type        = bool
  default     = true
}

variable "service_account_email" {
  description = "Service account email for the Cloud Run service (optional)"
  type        = string
  default     = ""
}

variable "labels" {
  description = "Labels to apply to the service"
  type        = map(string)
  default     = {}
}

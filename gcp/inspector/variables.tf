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

variable "environment" {
  description = "Deployment environment (e.g. production, staging, sandbox)"
  type        = string

  validation {
    condition     = length(trimspace(var.environment)) > 0
    error_message = "environment must be a non-empty string."
  }
}

variable "short_project_id" {
  description = "Short InsideOut project ID (first segment of UUID, e.g. 78476478) used for SA naming"
  type        = string

  validation {
    condition     = can(regex("^[0-9a-f]{8}$", var.short_project_id))
    error_message = "short_project_id must be an 8-character hex string (first segment of a UUID)."
  }
}

variable "deployment_sa_email" {
  description = "Deployment service account email that needs token creator permission on the inspector SA"
  type        = string

  validation {
    condition     = can(regex("@.*\\.iam\\.gserviceaccount\\.com$", var.deployment_sa_email))
    error_message = "deployment_sa_email must be a valid GCP service account email."
  }
}

variable "inspector_roles" {
  description = "Viewer roles to grant to the inspector service account"
  type        = list(string)
  default = [
    "roles/compute.viewer",
    "roles/container.viewer",
    "roles/run.viewer",
    "roles/cloudsql.viewer",
    "roles/storage.objectViewer",
    "roles/cloudkms.viewer",
    "roles/secretmanager.viewer",
    "roles/pubsub.viewer",
    "roles/logging.viewer",
    "roles/datastore.viewer",
    "roles/monitoring.viewer",
    "roles/iam.serviceAccountViewer",
  ]
}

variable "labels" {
  description = "Labels to apply to resources"
  type        = map(string)
  default     = {}
}

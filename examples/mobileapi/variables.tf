variable "gcp_cloud_logging_project" {
  type = string
}

variable "gcp_cloud_logging_region" {
  type = string
}

variable "gcp_cloud_run_cpu" {
  type = string
}

variable "gcp_cloud_run_max_instances" {
  type = number
}

variable "gcp_cloud_run_memory" {
  type = string
}

variable "gcp_cloud_run_min_instances" {
  type = number
}

variable "gcp_cloud_run_project" {
  type = string
}

variable "gcp_cloud_run_region" {
  type = string
}

variable "gcp_cloudsql_availability_type" {
  type = string
}

variable "gcp_cloudsql_project" {
  type = string
}

variable "gcp_cloudsql_region" {
  type = string
}

variable "gcp_cloudsql_tier" {
  type = string
}

variable "gcp_gcs_bucket_name" {
  type = string
}

variable "gcp_gcs_project" {
  type = string
}

variable "gcp_gcs_region" {
  type = string
}

variable "gcp_gcs_storage_class" {
  type = string
}

variable "gcp_gcs_versioning_enabled" {
  type = bool
}

variable "gcp_vpc_project" {
  type = string
}

variable "gcp_vpc_region" {
  type = string
}

variable "project" {
  description = "Project name prefix"
  type        = string
  default     = ""
}

variable "region" {
  description = "AWS region"
  type        = string
  default     = "us-west-2"
}

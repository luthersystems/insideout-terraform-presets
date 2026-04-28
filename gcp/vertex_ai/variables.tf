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
  description = "GCP region for the Vertex AI dataset"
  type        = string
  default     = "us-central1"
}

variable "dataset_name" {
  description = "Display name of the Vertex AI dataset"
  type        = string
  default     = "main-dataset"
}

variable "dataset_type" {
  description = "Dataset type. Picks the default metadata_schema_uri. One of: image, text, tabular, video, time_series."
  type        = string
  default     = "image"

  validation {
    condition     = contains(["image", "text", "tabular", "video", "time_series"], var.dataset_type)
    error_message = "dataset_type must be one of: image, text, tabular, video, time_series."
  }
}

variable "metadata_schema_uri" {
  description = "Override for metadata_schema_uri. Null picks a schema from dataset_type."
  type        = string
  default     = null

  validation {
    condition     = var.metadata_schema_uri == null ? true : startswith(var.metadata_schema_uri, "gs://google-cloud-aiplatform/schema/dataset/metadata/")
    error_message = "metadata_schema_uri must be a gs:// URI under gs://google-cloud-aiplatform/schema/dataset/metadata/."
  }
}

variable "encryption_kms_key_name" {
  description = "Fully-qualified KMS CMEK (projects/<p>/locations/<l>/keyRings/<k>/cryptoKeys/<c>) to encrypt the dataset. Null disables CMEK."
  type        = string
  default     = null
}

variable "labels" {
  description = "Labels to apply"
  type        = map(string)
  default     = {}
}

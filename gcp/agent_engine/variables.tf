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
  description = "GCP region for the Reasoning Engine (e.g. us-central1)."
  type        = string
  default     = "us-central1"
}

variable "display_name" {
  description = "Display name of the Reasoning Engine. Null defaults to \"<project>-agent-engine\" so a bare compose still produces a uniquely-named, attributable engine."
  type        = string
  default     = null
}

variable "package_artifact_uri" {
  description = "GCS URI (gs://<bucket>/<path>) of the packaged agent object the engine runs — the pickled Python object built and uploaded by the APPLICATION layer (this preset never builds it). Required: a fail-loud precondition rejects a null/non-gs:// value at plan time. When staging_bucket is wired, the artifact must live under it."
  type        = string
  default     = null

  # Shape is validated here; presence + bucket-membership are cross-variable
  # invariants enforced by preconditions on the engine resource (a per-variable
  # validation can only see its own variable, and "must be set" is clearer as a
  # resource precondition that names the engine that needs it).
  validation {
    condition     = var.package_artifact_uri == null ? true : can(regex("^gs://", var.package_artifact_uri))
    error_message = "package_artifact_uri must be a gs:// URI."
  }
}

variable "staging_bucket" {
  description = "GCS bucket URL (gs://<bucket>) the application stages the packaged artifact into. Wired from gcp/gcs.bucket_url in a full stack. Null on a standalone preview where the caller supplies an absolute package_artifact_uri. When set, package_artifact_uri must live under it (enforced by a precondition)."
  type        = string
  default     = null

  validation {
    condition     = var.staging_bucket == null ? true : can(regex("^gs://", var.staging_bucket))
    error_message = "staging_bucket must be a gs:// URI."
  }
}

variable "requirements_uri" {
  description = "Optional GCS URI (gs://<bucket>/requirements.txt) of the Python requirements file the engine installs alongside the packaged artifact. Null omits it — the engine uses only its bundled dependencies."
  type        = string
  default     = null

  validation {
    condition     = var.requirements_uri == null ? true : can(regex("^gs://", var.requirements_uri))
    error_message = "requirements_uri must be a gs:// URI."
  }
}

variable "dependency_files_uri" {
  description = "Optional GCS URI (gs://<bucket>/dependencies.tar.gz) of extra dependency files (tar.gz) the engine unpacks alongside the packaged artifact. Null omits it."
  type        = string
  default     = null

  validation {
    condition     = var.dependency_files_uri == null ? true : can(regex("^gs://", var.dependency_files_uri))
    error_message = "dependency_files_uri must be a gs:// URI."
  }
}

variable "python_version" {
  description = "Python runtime version for the packaged agent. Supported: 3.8, 3.9, 3.10, 3.11, 3.12, 3.13. Null lets the provider default (3.10) win."
  type        = string
  default     = null

  validation {
    condition     = var.python_version == null ? true : contains(["3.8", "3.9", "3.10", "3.11", "3.12", "3.13"], var.python_version)
    error_message = "python_version must be one of: 3.8, 3.9, 3.10, 3.11, 3.12, 3.13."
  }
}

variable "encryption_kms_key_name" {
  description = "Fully-qualified Cloud KMS CMEK (projects/<p>/locations/<l>/keyRings/<k>/cryptoKeys/<c>) to encrypt the engine and its sub-resources. Null disables CMEK (Google-managed encryption). The key must be in the same region as the engine."
  type        = string
  default     = null
}

variable "labels" {
  description = "Resource labels merged with the standard { project = var.project } identity label."
  type        = map(string)
  default     = {}
}

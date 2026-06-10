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
  description = "GCP region for the Vertex AI resources"
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

# --- Vector Search ----------------------------------------------------------

variable "enable_vector_search" {
  description = "When true, provision Vertex AI Vector Search (index + index endpoint + deployed index) alongside the dataset. The dataset is always created; this gates only the Vector Search resources."
  type        = bool
  default     = false
}

variable "index_dimensions" {
  description = "Dimensionality of the embedding vectors the index stores. IMMUTABLE — changing it forces index destroy/recreate. Must match the embedding model that produces the vectors (e.g. 768 for text-embedding-004)."
  type        = number
  default     = 768

  validation {
    condition     = var.index_dimensions > 0
    error_message = "index_dimensions must be greater than 0."
  }
}

variable "index_update_method" {
  description = "How embeddings are written to the index. BATCH (default) ingests from contents_delta_uri batches; STREAM upserts individual vectors via the streaming API. IMMUTABLE — changing it forces index destroy/recreate."
  type        = string
  default     = "BATCH_UPDATE"

  validation {
    # The Google API accepts the verbose forms BATCH_UPDATE / STREAM_UPDATE.
    condition     = contains(["BATCH_UPDATE", "STREAM_UPDATE"], var.index_update_method)
    error_message = "index_update_method must be one of: BATCH_UPDATE, STREAM_UPDATE."
  }
}

variable "contents_delta_uri" {
  description = "GCS prefix (gs://bucket/path) of embedding files used to seed/refresh the index. Null creates an empty index seeded out-of-band. Wired from gcp/gcs.bucket_url in a full stack."
  type        = string
  default     = null

  validation {
    condition     = var.contents_delta_uri == null ? true : startswith(var.contents_delta_uri, "gs://")
    error_message = "contents_delta_uri must be a gs:// URI."
  }
}

variable "index_distance_measure_type" {
  description = "Distance metric for nearest-neighbour search. One of DOT_PRODUCT_DISTANCE, COSINE_DISTANCE, SQUARED_L2_DISTANCE."
  type        = string
  default     = "DOT_PRODUCT_DISTANCE"

  validation {
    condition     = contains(["DOT_PRODUCT_DISTANCE", "COSINE_DISTANCE", "SQUARED_L2_DISTANCE"], var.index_distance_measure_type)
    error_message = "index_distance_measure_type must be one of: DOT_PRODUCT_DISTANCE, COSINE_DISTANCE, SQUARED_L2_DISTANCE."
  }
}

variable "index_approximate_neighbors_count" {
  description = "Number of neighbours to find via approximate search before exact reordering. Tuning knob; 150 is a sensible default for most embedding sizes."
  type        = number
  default     = 150

  validation {
    condition     = var.index_approximate_neighbors_count > 0
    error_message = "index_approximate_neighbors_count must be greater than 0."
  }
}

variable "network" {
  description = "Full VPC network resource name (projects/<project>/global/networks/<name>) for a private (VPC-peered) index endpoint. Null creates a public endpoint. Wired from gcp/vpc.vpc_id in a full stack; requires servicenetworking peering on the network (#600)."
  type        = string
  default     = null
}

variable "deployed_index_min_replicas" {
  description = "Minimum serving replicas for the deployed index."
  type        = number
  default     = 1

  validation {
    condition     = var.deployed_index_min_replicas > 0
    error_message = "deployed_index_min_replicas must be greater than 0."
  }
}

variable "deployed_index_max_replicas" {
  description = "Maximum serving replicas for the deployed index (autoscaling ceiling). Must be >= deployed_index_min_replicas (enforced by a precondition on the deployed-index resource)."
  type        = number
  default     = 2

  validation {
    condition     = var.deployed_index_max_replicas > 0
    error_message = "deployed_index_max_replicas must be greater than 0."
  }
}

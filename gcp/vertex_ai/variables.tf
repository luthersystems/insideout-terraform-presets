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
  description = "VPC network for a private (VPC-peered) index endpoint. Accepts the project-ID path (projects/<project_id>/global/networks/<name>, as emitted by gcp/vpc.vpc_id), the project-number path, or a bare network name — the preset extracts the network name and rebuilds the project-NUMBER path the API requires. Only used when enable_private_endpoint is also true; otherwise the endpoint is public."
  type        = string
  default     = null

  validation {
    # Accept null (public path), a bare network name (RFC1035-ish: starts with
    # a lowercase letter, lowercase letters/digits/hyphens), or the exact
    # projects/<project-id-or-number>/global/networks/<name> resource form.
    # Reject "" and malformed paths (trailing slash, wrong segments) at plan
    # time so local.network_name never silently parses to an empty/wrong name.
    condition = var.network == null ? true : (
      can(regex("^[a-z]([-a-z0-9]*[a-z0-9])?$", var.network)) ||
      can(regex("^projects/[a-z0-9-]+/global/networks/[a-z]([-a-z0-9]*[a-z0-9])?$", var.network))
    )
    error_message = "network must be null, a bare network name, or an exact projects/<id-or-number>/global/networks/<name> path."
  }
}

variable "enable_private_endpoint" {
  description = "When true AND a network is wired, the index endpoint is private (VPC-peered) instead of public. Default false = public endpoint, which works live today. The private path requires a servicenetworking PSC peering range on the network that gcp/vpc does not yet provision (follow-up #774); enabling it before #774 lands means the peering must exist out-of-band or a live apply fails ~30-90min into the deployed-index step."
  type        = bool
  default     = false
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

# --- Model serving (Endpoint + Model Garden) --------------------------------
# Issue #768. Independent of Vector Search: enable_serving gates a
# google_vertex_ai_endpoint, and (optionally) a one-shot Model Garden open-model
# deployment onto its own managed endpoint. The dataset and Vector Search
# resources are untouched by these flags.

variable "enable_serving" {
  description = "When true, provision a Vertex AI serving endpoint (google_vertex_ai_endpoint). With model_garden_model also set, additionally deploy that open model from Model Garden onto a managed endpoint. The dataset and Vector Search resources are unaffected by this flag."
  type        = bool
  default     = false
}

variable "model_garden_model" {
  description = "Model Garden publisher model to deploy when enable_serving is true. Format: publishers/<publisher>/models/<model>@<version> (e.g. publishers/google/models/gemma3@gemma-3-1b-it). Null provisions a bare endpoint with no model deployed (attach a model out-of-band)."
  type        = string
  default     = null

  validation {
    # Mirror the provider's documented publisher_model_name format
    # publishers/{publisher}/models/{publisher_model}@{version_id}. Reject a
    # bare model name or a Hugging Face id here so a malformed value fails at
    # plan time rather than ~30min into a live model deploy.
    condition     = var.model_garden_model == null ? true : can(regex("^publishers/[^/]+/models/[^/@]+@[^/@]+$", var.model_garden_model))
    error_message = "model_garden_model must be null or match publishers/<publisher>/models/<model>@<version> (e.g. publishers/google/models/gemma3@gemma-3-1b-it)."
  }
}

variable "model_garden_accept_eula" {
  description = "Whether the operator accepts the model's End User License Agreement (EULA / ToS). Many Model Garden open models (Gemma, Llama) require this to be true before they will deploy. Surfaced as model_config.accept_eula on the deployment."
  type        = bool
  default     = false
}

variable "serving_machine_type" {
  description = "Compute Engine machine type for the Model Garden deployment's dedicated serving resources. Defaults to a CPU-only machine (n1-standard-4); pick a GPU-capable type (e.g. g2-standard-16) when pairing with an accelerator."
  type        = string
  default     = "n1-standard-4"

  validation {
    condition     = can(regex("^[a-z][a-z0-9]*-[a-z0-9-]+$", var.serving_machine_type))
    error_message = "serving_machine_type must be a Compute Engine machine type (e.g. n1-standard-4, g2-standard-16)."
  }
}

variable "serving_accelerator_type" {
  description = "GPU/TPU accelerator type for the Model Garden deployment (e.g. NVIDIA_L4, NVIDIA_TESLA_T4). Null = CPU-only serving (the default). Must be paired with serving_accelerator_count > 0."
  type        = string
  default     = null

  validation {
    condition     = var.serving_accelerator_type == null ? true : can(regex("^[A-Z][A-Z0-9_]+$", var.serving_accelerator_type))
    error_message = "serving_accelerator_type must be null or an uppercase accelerator enum (e.g. NVIDIA_L4)."
  }
}

variable "serving_accelerator_count" {
  description = "Number of accelerators to attach per serving replica. 0 (default) = CPU-only. Must be > 0 when serving_accelerator_type is set (enforced by a precondition on the deployment)."
  type        = number
  default     = 0

  validation {
    condition     = var.serving_accelerator_count >= 0
    error_message = "serving_accelerator_count must be >= 0."
  }
}

variable "serving_min_replicas" {
  description = "Minimum serving replicas for the Model Garden deployment's dedicated resources."
  type        = number
  default     = 1

  validation {
    condition     = var.serving_min_replicas > 0
    error_message = "serving_min_replicas must be greater than 0."
  }
}

variable "serving_max_replicas" {
  description = "Maximum serving replicas (autoscaling ceiling) for the Model Garden deployment. Must be >= serving_min_replicas (enforced by a precondition on the deployment)."
  type        = number
  default     = 1

  validation {
    condition     = var.serving_max_replicas > 0
    error_message = "serving_max_replicas must be greater than 0."
  }
}

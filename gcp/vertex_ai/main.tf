# GCP Vertex AI
# - Dataset (always created): https://registry.terraform.io/providers/hashicorp/google/latest/docs/resources/vertex_ai_dataset
# - Vector Search (gated on var.enable_vector_search): index + index endpoint +
#   deployed index. https://registry.terraform.io/providers/hashicorp/google/latest/docs/resources/vertex_ai_index
#
# Vector Search composes a managed nearest-neighbour vector DB:
#   google_vertex_ai_index                          — the ANN index (embeddings)
#   google_vertex_ai_index_endpoint                 — the serving endpoint
#   google_vertex_ai_index_endpoint_deployed_index  — binds index -> endpoint
#
# Serving endpoint: PUBLIC by default. The endpoint goes private (VPC-peered)
# only when a network is wired AND var.enable_private_endpoint is set true.
# Private serving requires a servicenetworking PSC peering range on the network
# (google_service_networking_connection + a VPC_PEERING global address) that
# gcp/vpc does not yet provision (#600 / follow-up #774); until that lands a
# live private apply fails ~30-90min in. So the default composed path is a
# public endpoint, which works live today. See var.enable_private_endpoint.
#
# Network form: google_vertex_ai_index_endpoint.network requires the
# project-NUMBER form projects/<PROJECT_NUMBER>/global/networks/<name>. The
# wired vpc_id (and any human-supplied value) is typically the project-ID form
# projects/<PROJECT_ID>/global/networks/<name> or a bare network name, so we
# parse out the network NAME and rebuild the canonical project-number path with
# data.google_project.this.number. See local.network_canonical.

terraform {
  required_version = ">= 1.5"
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = ">= 5.0"
    }
  }
}

locals {
  metadata_schemas = {
    image       = "gs://google-cloud-aiplatform/schema/dataset/metadata/image_1.0.0.yaml"
    text        = "gs://google-cloud-aiplatform/schema/dataset/metadata/text_1.0.0.yaml"
    tabular     = "gs://google-cloud-aiplatform/schema/dataset/metadata/tabular_1.0.0.yaml"
    video       = "gs://google-cloud-aiplatform/schema/dataset/metadata/video_1.0.0.yaml"
    time_series = "gs://google-cloud-aiplatform/schema/dataset/metadata/time_series_1.0.0.yaml"
  }

  resolved_schema_uri = coalesce(var.metadata_schema_uri, local.metadata_schemas[var.dataset_type])

  # The endpoint is private (VPC-peered) only when a network is wired AND the
  # operator opts in via enable_private_endpoint. Default is a public endpoint
  # (works live today; private needs the #774 PSC peering range first). The
  # consuming resource is already count-gated on enable_vector_search, so this
  # local does not re-test that flag.
  vector_search_private = var.network != null && var.enable_private_endpoint

  # google_vertex_ai_index_endpoint.network needs the project-NUMBER form
  # projects/<NUMBER>/global/networks/<name>. Extract the bare network NAME from
  # whatever was supplied (full project-ID path, full project-number path, or a
  # bare name) and rebuild the canonical path with the project number. Only
  # evaluated on the private path; null otherwise.
  network_name = var.network == null ? null : element(split("/", var.network), length(split("/", var.network)) - 1)
  network_canonical = local.vector_search_private ? (
    "projects/${data.google_project.this.number}/global/networks/${local.network_name}"
  ) : null
}

# Resolves the caller's project to its numeric project number, which the
# index-endpoint network path requires (the wired vpc_id carries the project ID,
# not the number). Always read so the data source plans cleanly; only consumed
# on the private path.
data "google_project" "this" {
  project_id = var.project_id
}

resource "google_vertex_ai_dataset" "dataset" {
  display_name        = var.dataset_name
  metadata_schema_uri = local.resolved_schema_uri
  region              = var.region
  project             = var.project_id

  dynamic "encryption_spec" {
    for_each = var.encryption_kms_key_name == null ? [] : [var.encryption_kms_key_name]
    content {
      kms_key_name = encryption_spec.value
    }
  }

  labels = merge(
    {
      project = var.project
    },
    var.labels
  )
}

# --- Vector Search ----------------------------------------------------------

# The ANN index. Dimensions and update_method are immutable: the Google API
# rejects in-place changes to either, so terraform must destroy/recreate the
# index when they change. The preset surfaces both as their own variables with
# plan-time validation so a bad value fails fast rather than at apply.
resource "google_vertex_ai_index" "this" {
  count               = var.enable_vector_search ? 1 : 0
  project             = var.project_id
  region              = var.region
  display_name        = "${var.project}-vector-index"
  description         = "InsideOut Vector Search index for ${var.project}"
  index_update_method = var.index_update_method

  metadata {
    # contents_delta_uri points at a GCS prefix of embedding files used to seed
    # the index. Null on a standalone preview (no GCS wired) — the index is
    # created empty and embeddings are upserted out-of-band.
    contents_delta_uri = var.contents_delta_uri

    config {
      dimensions                  = var.index_dimensions
      approximate_neighbors_count = var.index_approximate_neighbors_count
      distance_measure_type       = var.index_distance_measure_type

      algorithm_config {
        tree_ah_config {
          leaf_node_embedding_count    = 1000
          leaf_nodes_to_search_percent = 10
        }
      }
    }
  }

  labels = merge(
    {
      project = var.project
    },
    var.labels
  )
}

# The serving endpoint. PUBLIC by default; private (VPC-peered) only when a
# network is wired AND var.enable_private_endpoint is set.
resource "google_vertex_ai_index_endpoint" "this" {
  count        = var.enable_vector_search ? 1 : 0
  project      = var.project_id
  region       = var.region
  display_name = "${var.project}-vector-endpoint"
  description  = "InsideOut Vector Search endpoint for ${var.project}"

  # VPC peering path: network is the canonical project-NUMBER resource name
  # projects/<NUMBER>/global/networks/<name>, rebuilt from the wired vpc_id
  # (which carries the project ID) via data.google_project.this.number.
  #
  # NOTE: the private path additionally requires a servicenetworking PSC
  # peering range on this network (google_service_networking_connection + a
  # VPC_PEERING global address) that gcp/vpc does not yet provision (#774).
  # Until that lands, an opt-in private apply needs the peering created
  # out-of-band. The default (public) path is unaffected.
  network = local.network_canonical

  # Public unless the private path is selected.
  public_endpoint_enabled = !local.vector_search_private

  labels = merge(
    {
      project = var.project
    },
    var.labels
  )
}

# Binds the index to the endpoint. The deploy is the long pole: Vertex spins up
# serving replicas, which routinely takes 30-60 minutes, so create has a 90m
# timeout to stay well clear of the provider's shorter default.
resource "google_vertex_ai_index_endpoint_deployed_index" "this" {
  count          = var.enable_vector_search ? 1 : 0
  region         = var.region
  index_endpoint = google_vertex_ai_index_endpoint.this[0].id
  index          = google_vertex_ai_index.this[0].id
  # The API requires deployed_index_id to start with a letter and contain only
  # [a-z0-9_]. Lowercase var.project, replace every invalid char with "_", and
  # prefix "idx_" so a project that starts with a digit (or is otherwise
  # awkward) still yields a valid id.
  deployed_index_id = "idx_${replace(lower(var.project), "/[^a-z0-9_]/", "_")}_vector"
  display_name      = "${var.project}-vector-deployed"

  automatic_resources {
    min_replica_count = var.deployed_index_min_replicas
    max_replica_count = var.deployed_index_max_replicas
  }

  timeouts {
    create = "90m"
  }

  lifecycle {
    # Cross-variable invariant lives here (not in a variable validation block)
    # because a per-variable validation may only reference its own variable;
    # the autoscaling ceiling must not sit below the floor.
    precondition {
      condition     = var.deployed_index_max_replicas >= var.deployed_index_min_replicas
      error_message = "deployed_index_max_replicas must be >= deployed_index_min_replicas."
    }
  }
}

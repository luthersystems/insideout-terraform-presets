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
# Private serving: when var.network is supplied the endpoint is created on the
# VPC peering path (its public_endpoint_enabled is left false). This is the
# reason gcp/vertex_ai carries a hard dependency on gcp/vpc (contracts.go:322 /
# #600): a private index endpoint requires servicenetworking peering on the
# network, otherwise the deployed index errors with NOT_FOUND on the peering
# range. On a single-module preview (no network wired) the endpoint falls back
# to a public endpoint so the preset still composes/plans standalone.

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

  # A private (VPC-peered) endpoint is used only when a network is wired in.
  # Standalone previews have no network and fall back to a public endpoint so
  # the preset still plans without the VPC peering range existing.
  vector_search_private = var.enable_vector_search && var.network != null
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

# The serving endpoint. Private (VPC-peered) when var.network is wired,
# otherwise public so a standalone preview still composes.
resource "google_vertex_ai_index_endpoint" "this" {
  count        = var.enable_vector_search ? 1 : 0
  project      = var.project_id
  region       = var.region
  display_name = "${var.project}-vector-endpoint"
  description  = "InsideOut Vector Search endpoint for ${var.project}"

  # VPC peering path: network is the full resource name
  # projects/<project>/global/networks/<name>. Wired from gcp/vpc.vpc_id.
  #
  # NOTE: a private (VPC-peered) endpoint requires a servicenetworking
  # PSC peering range on this network (google_service_networking_connection +
  # a VPC_PEERING global address). That peering is the VPC's responsibility
  # (the reason gcp/vertex_ai hard-depends on gcp/vpc — #600); it is not
  # provisioned here. Until gcp/vpc provisions that range, real applies on the
  # private path will need the peering created out-of-band. Compose/plan and
  # the public-endpoint path are unaffected.
  network = local.vector_search_private ? var.network : null

  # Public endpoint only when no private network is wired.
  public_endpoint_enabled = local.vector_search_private ? false : true

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
  count             = var.enable_vector_search ? 1 : 0
  region            = var.region
  index_endpoint    = google_vertex_ai_index_endpoint.this[0].id
  index             = google_vertex_ai_index.this[0].id
  deployed_index_id = replace("${var.project}_vector_idx", "-", "_")
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

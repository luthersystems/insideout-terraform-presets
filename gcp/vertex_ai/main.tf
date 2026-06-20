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
  #
  # Parse the name out of the .../networks/<name> path with a regex capture so a
  # malformed value can't silently yield the wrong segment (var.network is
  # validated to be a bare name or an exact projects/.../networks/<name> path,
  # so a non-match here means it was a bare name — fall back to the value
  # itself). element()-last was unsafe: a trailing slash or short path would
  # parse to "" or the wrong segment with no plan-time signal.
  network_name = var.network == null ? null : try(
    regex("/networks/([^/]+)$", var.network)[0],
    var.network,
  )
  network_canonical = local.vector_search_private ? (
    "projects/${data.google_project.this.number}/global/networks/${local.network_name}"
  ) : null

  # --- Model serving (#768) -------------------------------------------------
  # google_vertex_ai_endpoint.name must be NUMERIC, no leading zeros, <=10
  # digits (the endpoint's resource id). Derive a deterministic number from
  # var.project so the name is stable across applies for a given stack: parse
  # the first 8 hex chars of sha256(project) -> an integer <= 4294967295 (10
  # digits max). tostring renders an int without leading zeros, so the API
  # constraint holds.
  serving_endpoint_name = tostring(parseint(substr(sha256(var.project), 0, 8), 16))

  # The Model Garden one-shot deployment is created only when serving is on AND
  # a model is named. It provisions its OWN managed endpoint (with the model
  # deployed onto it), so the bare endpoint below is mutually exclusive with it:
  # creating both would leave an empty bare endpoint sitting next to the real
  # one the model lives on, and the outputs would point at the empty shell.
  model_garden_enabled = var.enable_serving && var.model_garden_model != null

  # The bare serving endpoint is the enable_serving-WITHOUT-a-model path: a
  # generic attach point for custom/other models, deployed out-of-band. When a
  # Model Garden model IS named, that resource owns the endpoint instead, so the
  # bare endpoint is NOT created (mutually exclusive count-gate).
  bare_endpoint_enabled = var.enable_serving && var.model_garden_model == null

  # Attach dedicated accelerators only when an accelerator type is requested.
  # CPU-only serving (the default) omits the machine_spec accelerator fields so
  # the deployment does not demand GPU quota it doesn't need.
  serving_accelerator_enabled = var.serving_accelerator_type != null
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
  # The private path additionally requires a servicenetworking PSC peering
  # range on this network (google_service_networking_connection + a VPC_PEERING
  # global address). gcp/vpc now provisions this when its
  # enable_service_networking flag is set (#774), and the composer wires the
  # connection id into var.service_networking_connection so the peering is
  # created before this endpoint applies. The default (public) path is
  # unaffected and needs none of it.
  network = local.network_canonical

  # Public unless the private path is selected.
  public_endpoint_enabled = !local.vector_search_private

  labels = merge(
    {
      project = var.project
    },
    var.labels
  )

  lifecycle {
    # Fail-loud: a private (VPC-peered) endpoint cannot serve until the
    # servicenetworking PSC peering range exists on the network, or the apply
    # fails ~30-90min into the deployed-index step. Referencing
    # var.service_networking_connection here both surfaces the missing peering
    # at plan time AND creates the dependency edge (the composer wires it from
    # gcp/vpc.service_networking_connection_id) so Terraform orders the peering
    # before this endpoint. Public path is exempt (it needs no peering). #774.
    precondition {
      condition     = local.vector_search_private ? var.service_networking_connection != null : true
      error_message = "A private Vertex AI index endpoint (enable_private_endpoint = true with a wired network) requires a servicenetworking PSC peering range. Set gcp/vpc's enable_service_networking = true so service_networking_connection is wired, or provision the peering out-of-band (#774/#600)."
    }
  }
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

# --- Model serving (Endpoint + Model Garden) — issue #768 -------------------
#
# google_vertex_ai_endpoint:                            bare serving endpoint (a
#   managed front door for online predictions). Created when enable_serving is
#   true AND no Model Garden model is named — the generic attach point for
#   custom models deployed out-of-band. PUBLIC by default; the private-endpoint
#   story is unchanged here (it reuses the same #774 PSC peering range the
#   Vector Search endpoint needs and is out of scope for this issue).
# google_vertex_ai_endpoint_with_model_garden_deployment: one-shot deploy of an
#   open Model Garden model (Gemma/Llama/etc) onto its OWN managed endpoint.
#   Created when enable_serving is true AND model_garden_model is named. The
#   deploy is the long pole — model downloads + replica spin-up routinely take
#   30+ minutes — so create/delete carry generous 60m timeouts.
#
# The two are MUTUALLY EXCLUSIVE: model_garden manages its own endpoint, so when
# a model is named the bare endpoint is suppressed. Otherwise the bare endpoint
# would sit empty next to the real one and the endpoint_id/endpoint_name outputs
# would point at the empty shell (the bug fixed under #768 review). The outputs
# return whichever endpoint actually exists.
#
# Resources verified against hashicorp/google v7.x (GA tier; both resources are
# in the GA provider — see PR notes). The dataset and Vector Search resources
# above are untouched by enable_serving.

# A bare serving endpoint, the generic attach point for custom/other models
# deployed out-of-band. Created ONLY on the enable_serving-without-a-model path:
# when a Model Garden model is named, the model_garden resource below provisions
# its own endpoint with the model on it, so this bare one is suppressed (they are
# mutually exclusive — see local.bare_endpoint_enabled / local.model_garden_enabled).
resource "google_vertex_ai_endpoint" "serving" {
  count        = local.bare_endpoint_enabled ? 1 : 0
  name         = local.serving_endpoint_name
  display_name = "${var.project}-serving-endpoint"
  description  = "InsideOut Vertex AI serving endpoint for ${var.project}"
  # location is the required, canonical field on this resource (region is a
  # legacy alias and redundant when location is set). The sibling dataset/index
  # resources only expose region, hence the differing field here.
  location = var.region
  project  = var.project_id

  labels = merge(
    {
      project = var.project
    },
    var.labels
  )
}

# One-shot Model Garden open-model deployment onto a managed endpoint. EULA
# acceptance and accelerator quota are operator concerns (see var docs): many
# open models won't deploy until model_garden_accept_eula is true, and a GPU
# accelerator_type needs matching quota in the project/region.
resource "google_vertex_ai_endpoint_with_model_garden_deployment" "model_garden" {
  count                = local.model_garden_enabled ? 1 : 0
  publisher_model_name = var.model_garden_model
  location             = var.region
  project              = var.project_id

  model_config {
    accept_eula = var.model_garden_accept_eula
  }

  deploy_config {
    dedicated_resources {
      min_replica_count = var.serving_min_replicas
      max_replica_count = var.serving_max_replicas

      machine_spec {
        machine_type = var.serving_machine_type
        # Accelerator fields only when a GPU/TPU type is requested; CPU-only
        # serving leaves them unset so no accelerator quota is demanded.
        accelerator_type  = local.serving_accelerator_enabled ? var.serving_accelerator_type : null
        accelerator_count = local.serving_accelerator_enabled ? var.serving_accelerator_count : null
      }
    }
  }

  # Model deploys run long (download + replica spin-up). Stay well clear of the
  # provider's shorter defaults.
  timeouts {
    create = "60m"
    delete = "60m"
  }

  lifecycle {
    # Cross-variable invariants live here (a per-variable validation may only
    # reference its own variable):
    #   - autoscaling ceiling must not sit below the floor;
    #   - an accelerator TYPE without a positive COUNT (or vice versa) is a
    #     half-configured GPU request that the API would reject at apply.
    precondition {
      condition     = var.serving_max_replicas >= var.serving_min_replicas
      error_message = "serving_max_replicas must be >= serving_min_replicas."
    }
    precondition {
      condition     = (var.serving_accelerator_type == null) == (var.serving_accelerator_count == 0)
      error_message = "serving_accelerator_type and serving_accelerator_count must be set together: a type requires count > 0, and count > 0 requires a type."
    }
  }
}

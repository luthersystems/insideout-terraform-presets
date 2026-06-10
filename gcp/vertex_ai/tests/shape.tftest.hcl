mock_provider "google" {}

# Issue #764 (gcp/vertex_ai Vector Search) shape tests. Verifies that:
#   - Defaults compose just the dataset; Vector Search is OFF by default and
#     emits zero index/endpoint/deployed-index resources.
#   - enable_vector_search=true composes index + endpoint + deployed index,
#     wires a private endpoint when a network is supplied, and carries the
#     90m create timeout on the deployed index.
#   - The immutable knobs (index_dimensions, index_update_method) and the
#     other validations reject obvious misconfigurations at plan time.

run "defaults_dataset_only" {
  command = plan

  variables {
    project    = "test"
    project_id = "test-project"
  }

  # Dataset is always created.
  assert {
    condition     = google_vertex_ai_dataset.dataset.display_name == "main-dataset"
    error_message = "dataset must be created unconditionally with its default display name."
  }

  # Vector Search is off by default -> no index/endpoint/deployed-index.
  assert {
    condition     = length(google_vertex_ai_index.this) == 0
    error_message = "Vector Search index must NOT be created when enable_vector_search is false (default)."
  }

  assert {
    condition     = length(google_vertex_ai_index_endpoint.this) == 0
    error_message = "Vector Search endpoint must NOT be created when enable_vector_search is false (default)."
  }

  assert {
    condition     = length(google_vertex_ai_index_endpoint_deployed_index.this) == 0
    error_message = "Deployed index must NOT be created when enable_vector_search is false (default)."
  }
}

run "vector_search" {
  command = plan

  variables {
    project              = "test"
    project_id           = "test-project"
    enable_vector_search = true
    index_dimensions     = 768
    contents_delta_uri   = "gs://test-bucket/embeddings"
    network              = "projects/test-project/global/networks/test-vpc"
  }

  # All three Vector Search resources compose.
  assert {
    condition     = length(google_vertex_ai_index.this) == 1
    error_message = "enable_vector_search=true must create exactly one index."
  }

  assert {
    condition     = length(google_vertex_ai_index_endpoint.this) == 1
    error_message = "enable_vector_search=true must create exactly one index endpoint."
  }

  assert {
    condition     = length(google_vertex_ai_index_endpoint_deployed_index.this) == 1
    error_message = "enable_vector_search=true must create exactly one deployed index."
  }

  # Index dimensions flow through to the metadata.config block.
  assert {
    condition     = google_vertex_ai_index.this[0].metadata[0].config[0].dimensions == 768
    error_message = "index_dimensions must reach metadata.config.dimensions."
  }

  # Index display name carries the var.project prefix (name-prefix scoping).
  assert {
    condition     = google_vertex_ai_index.this[0].display_name == "test-vector-index"
    error_message = "index display_name must be var.project-prefixed (lint-labelless-name-prefix contract)."
  }

  # A supplied network -> private endpoint (public disabled, network set).
  assert {
    condition     = google_vertex_ai_index_endpoint.this[0].network == "projects/test-project/global/networks/test-vpc"
    error_message = "a wired network must drive the private (VPC-peered) endpoint path."
  }

  assert {
    condition     = google_vertex_ai_index_endpoint.this[0].public_endpoint_enabled == false
    error_message = "endpoint must be private (public disabled) when a network is wired."
  }

  # contents_delta_uri flows through to seed the index.
  assert {
    condition     = google_vertex_ai_index.this[0].metadata[0].contents_delta_uri == "gs://test-bucket/embeddings"
    error_message = "contents_delta_uri must reach metadata.contents_delta_uri."
  }

  # deployed_index_id is sanitized (hyphens -> underscores) to satisfy the API.
  assert {
    condition     = google_vertex_ai_index_endpoint_deployed_index.this[0].deployed_index_id == "test_vector_idx"
    error_message = "deployed_index_id must be sanitized to [a-z0-9_]."
  }

  # 90m create timeout on the long-running deploy.
  assert {
    condition     = google_vertex_ai_index_endpoint_deployed_index.this[0].timeouts.create == "90m"
    error_message = "deployed index must carry a 90m create timeout (deploys run 30-60min)."
  }
}

run "vector_search_public_endpoint_without_network" {
  command = plan

  variables {
    project              = "test"
    project_id           = "test-project"
    enable_vector_search = true
  }

  # No network wired -> public endpoint so a standalone preview still composes.
  assert {
    condition     = google_vertex_ai_index_endpoint.this[0].public_endpoint_enabled == true
    error_message = "endpoint must fall back to public when no network is wired (standalone preview)."
  }

  assert {
    condition     = google_vertex_ai_index_endpoint.this[0].network == null
    error_message = "endpoint network must be null when no network is wired."
  }
}

# -----------------------------------------------------------------------------
# Negative cases: validation blocks must reject misconfigurations at plan time.
# -----------------------------------------------------------------------------

run "rejects_zero_dimensions" {
  command = plan

  variables {
    project          = "test"
    project_id       = "test-project"
    index_dimensions = 0
  }

  expect_failures = [var.index_dimensions]
}

run "rejects_negative_dimensions" {
  command = plan

  variables {
    project          = "test"
    project_id       = "test-project"
    index_dimensions = -1
  }

  expect_failures = [var.index_dimensions]
}

run "rejects_invalid_update_method" {
  command = plan

  variables {
    project             = "test"
    project_id          = "test-project"
    index_update_method = "STREAM" # API expects STREAM_UPDATE
  }

  expect_failures = [var.index_update_method]
}

run "rejects_non_gcs_contents_uri" {
  command = plan

  variables {
    project            = "test"
    project_id         = "test-project"
    contents_delta_uri = "s3://wrong/scheme"
  }

  expect_failures = [var.contents_delta_uri]
}

run "rejects_max_replicas_below_min" {
  command = plan

  variables {
    project                     = "test"
    project_id                  = "test-project"
    enable_vector_search        = true
    deployed_index_min_replicas = 3
    deployed_index_max_replicas = 1 # ceiling below floor
  }

  # The cross-variable invariant is a resource precondition, not a variable
  # validation, so the failure surfaces on the deployed-index resource.
  expect_failures = [google_vertex_ai_index_endpoint_deployed_index.this]
}

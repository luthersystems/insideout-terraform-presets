mock_provider "google" {}

# Issue #764 (gcp/vertex_ai Vector Search) shape tests. Verifies that:
#   - Defaults compose just the dataset; Vector Search is OFF by default and
#     emits zero index/endpoint/deployed-index resources.
#   - enable_vector_search=true composes index + endpoint + deployed index,
#     defaults to a PUBLIC endpoint even with a network wired, goes private only
#     when enable_private_endpoint is also set, and carries the 90m create
#     timeout on the deployed index.
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
    contents_delta_uri   = "gs://test-bucket/vertex-index/"
    # Network wired but enable_private_endpoint left at its false default: the
    # endpoint must stay PUBLIC (private requires the #774 PSC peering range).
    network = "projects/test-project/global/networks/test-vpc"
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

  # Default (no enable_private_endpoint): PUBLIC even with a network wired, and
  # no network is set on the endpoint (private path is opt-in only).
  assert {
    condition     = google_vertex_ai_index_endpoint.this[0].public_endpoint_enabled == true
    error_message = "endpoint must default to PUBLIC even when a network is wired (private is opt-in via enable_private_endpoint)."
  }

  assert {
    condition     = google_vertex_ai_index_endpoint.this[0].network == null
    error_message = "endpoint network must be null on the default public path (private is opt-in)."
  }

  # contents_delta_uri flows through to seed the index from the dedicated prefix.
  assert {
    condition     = google_vertex_ai_index.this[0].metadata[0].contents_delta_uri == "gs://test-bucket/vertex-index/"
    error_message = "contents_delta_uri must reach metadata.contents_delta_uri."
  }

  # deployed_index_id is API-safe: starts with a letter, only [a-z0-9_].
  assert {
    condition     = google_vertex_ai_index_endpoint_deployed_index.this[0].deployed_index_id == "idx_test_vector"
    error_message = "deployed_index_id must be sanitized to a letter-leading [a-z0-9_] id (idx_<project>_vector)."
  }

  # 90m create timeout on the long-running deploy.
  assert {
    condition     = google_vertex_ai_index_endpoint_deployed_index.this[0].timeouts.create == "90m"
    error_message = "deployed index must carry a 90m create timeout (deploys run 30-60min)."
  }
}

run "vector_search_private_opt_in" {
  command = plan

  # Pin data.google_project.this.number to a fixed value so the rebuilt
  # canonical network path can be asserted as an EXACT literal (otherwise the
  # mock provider supplies a random computed number and the test could only
  # assert prefix/suffix — which a passthrough rebuild would survive).
  override_data {
    target = data.google_project.this
    values = {
      number = "123456789012"
    }
  }

  variables {
    project                 = "test"
    project_id              = "test-project"
    enable_vector_search    = true
    enable_private_endpoint = true
    # Project-ID full path: the parser must keep the network NAME and the
    # rebuild must swap the project ID for the pinned project NUMBER.
    network = "projects/test-project/global/networks/test-vpc"
  }

  # network + enable_private_endpoint -> private endpoint (public disabled).
  assert {
    condition     = google_vertex_ai_index_endpoint.this[0].public_endpoint_enabled == false
    error_message = "endpoint must be private (public disabled) when a network is wired AND enable_private_endpoint is set."
  }

  # The endpoint network is rebuilt into the project-NUMBER path the API
  # requires: the name survives, the pinned number replaces the project ID.
  # Asserting the EXACT literal kills a passthrough mutation of the rebuild.
  assert {
    condition     = google_vertex_ai_index_endpoint.this[0].network == "projects/123456789012/global/networks/test-vpc"
    error_message = "private endpoint network must rebuild to projects/<number>/global/networks/<name> using the project NUMBER (not the project ID)."
  }
}

run "vector_search_private_bare_network_name" {
  command = plan

  # Bare-name input ("my-vpc") with the project number pinned so the rebuilt
  # path can be asserted EXACTLY. Exercises the regex bare-name fallback and
  # the project-NUMBER rebuild — a passthrough that skipped the rebuild would
  # leave network = "my-vpc" and fail this assert.
  override_data {
    target = data.google_project.this
    values = {
      number = "123456789012"
    }
  }

  variables {
    project                 = "test"
    project_id              = "test-project"
    enable_vector_search    = true
    enable_private_endpoint = true
    network                 = "my-vpc"
  }

  assert {
    condition     = google_vertex_ai_index_endpoint.this[0].network == "projects/123456789012/global/networks/my-vpc"
    error_message = "a bare network name must be rebuilt to the full projects/<number>/global/networks/<name> form."
  }
}

run "vector_search_private_opt_in_without_network_stays_public" {
  command = plan

  variables {
    project                 = "test"
    project_id              = "test-project"
    enable_vector_search    = true
    enable_private_endpoint = true
    # No network wired: the flag alone cannot force private.
  }

  assert {
    condition     = google_vertex_ai_index_endpoint.this[0].public_endpoint_enabled == true
    error_message = "endpoint must stay PUBLIC when enable_private_endpoint is set but no network is wired."
  }

  assert {
    condition     = google_vertex_ai_index_endpoint.this[0].network == null
    error_message = "endpoint network must be null when no network is wired, regardless of enable_private_endpoint."
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

run "deployed_index_id_sanitizes_dirty_project" {
  command = plan

  variables {
    # Project with uppercase + a dot: both are illegal in a deployed_index_id
    # (must start with a letter, only [a-z0-9_]). The default "test" project is
    # already clean and never exercised the replace(); this one does.
    project              = "My-Proj.1"
    project_id           = "test-project"
    enable_vector_search = true
  }

  assert {
    condition     = google_vertex_ai_index_endpoint_deployed_index.this[0].deployed_index_id == "idx_my_proj_1_vector"
    error_message = "deployed_index_id must lowercase var.project and replace every non-[a-z0-9_] char with '_' (My-Proj.1 -> my_proj_1)."
  }
}

run "deployed_index_min_eq_max_composes" {
  command = plan

  variables {
    project              = "test"
    project_id           = "test-project"
    enable_vector_search = true
    # Equal floor/ceiling is the >= boundary: it MUST compose (pins the
    # precondition against a strict-> mutation that would reject min == max).
    deployed_index_min_replicas = 2
    deployed_index_max_replicas = 2
  }

  assert {
    condition     = length(google_vertex_ai_index_endpoint_deployed_index.this) == 1
    error_message = "deployed index must compose when max_replicas == min_replicas (the >= boundary is inclusive)."
  }

  assert {
    condition     = google_vertex_ai_index_endpoint_deployed_index.this[0].automatic_resources[0].min_replica_count == 2
    error_message = "min_replica_count must flow through to automatic_resources."
  }
}

# -----------------------------------------------------------------------------
# Alert policy: the per-component query-latency alarm is emitted only when
# Vector Search AND observability are both on, and carries the verified metric.
# -----------------------------------------------------------------------------

run "alert_policy_emitted_when_vector_search_and_observability_on" {
  command = plan

  variables {
    project              = "test"
    project_id           = "test-project"
    enable_vector_search = true
    enable_observability = true
  }

  assert {
    condition     = length(google_monitoring_alert_policy.vector_search_query_latency_high) == 1
    error_message = "query-latency alert policy must be emitted when Vector Search AND observability are both enabled."
  }

  # The filter must carry the metric.type verified against Google's official
  # metrics list (matching_engine/query/latencies, slashes not underscore).
  assert {
    condition = strcontains(
      google_monitoring_alert_policy.vector_search_query_latency_high["0"].conditions[0].condition_threshold[0].filter,
      "metric.type=\"aiplatform.googleapis.com/matching_engine/query/latencies\""
    )
    error_message = "alert policy filter must reference the verified matching_engine/query/latencies metric type."
  }
}

run "alert_policy_absent_when_vector_search_off" {
  command = plan

  variables {
    project              = "test"
    project_id           = "test-project"
    enable_vector_search = false
    enable_observability = true
  }

  assert {
    condition     = length(google_monitoring_alert_policy.vector_search_query_latency_high) == 0
    error_message = "no alert policy when Vector Search is off — the bare dataset has no serving surface to alarm on."
  }
}

# -----------------------------------------------------------------------------
# Model serving (#768): endpoint + Model Garden deployment, gated on
# enable_serving. The dataset and Vector Search resources stay untouched.
# -----------------------------------------------------------------------------

run "serving_off_by_default" {
  command = plan

  variables {
    project    = "test"
    project_id = "test-project"
  }

  # Serving is off by default -> no endpoint, no model-garden deployment.
  assert {
    condition     = length(google_vertex_ai_endpoint.serving) == 0
    error_message = "serving endpoint must NOT be created when enable_serving is false (default)."
  }

  assert {
    condition     = length(google_vertex_ai_endpoint_with_model_garden_deployment.model_garden) == 0
    error_message = "model-garden deployment must NOT be created when enable_serving is false (default)."
  }

  # And no serving alarms.
  assert {
    condition     = length(google_monitoring_alert_policy.serving_prediction_latency_high) == 0
    error_message = "serving prediction-latency alarm must be absent when serving is off."
  }

  # Serving outputs are null when serving is off (guards the [0]-index-on-empty
  # short-circuit in outputs.tf).
  assert {
    condition     = output.endpoint_id == null
    error_message = "endpoint_id output must be null when serving is disabled."
  }

  assert {
    condition     = output.endpoint_name == null
    error_message = "endpoint_name output must be null when serving is disabled."
  }
}

run "serving" {
  command = plan

  variables {
    project        = "test"
    project_id     = "test-project"
    enable_serving = true
    # No model named -> a bare endpoint, no model-garden deployment.
  }

  # Exactly one bare serving endpoint, no model-garden deployment.
  assert {
    condition     = length(google_vertex_ai_endpoint.serving) == 1
    error_message = "enable_serving=true must create exactly one serving endpoint."
  }

  assert {
    condition     = length(google_vertex_ai_endpoint_with_model_garden_deployment.model_garden) == 0
    error_message = "no model named -> no model-garden deployment (bare endpoint path)."
  }

  # Endpoint name is the deterministic NUMERIC id the API requires (no leading
  # zeros, <=10 digits). Asserting the exact literal pins the sha256-derived
  # name against a mutation of the derivation:
  # parseint(substr(sha256("test"),0,8),16) = 2676412545.
  assert {
    condition     = google_vertex_ai_endpoint.serving[0].name == "2676412545"
    error_message = "endpoint name must be the deterministic numeric id derived from sha256(var.project)."
  }

  assert {
    condition     = can(regex("^[1-9][0-9]{0,9}$", google_vertex_ai_endpoint.serving[0].name))
    error_message = "endpoint name must be numeric, no leading zeros, <=10 digits (the API constraint)."
  }

  # Display name carries the var.project prefix (name-prefix scoping).
  assert {
    condition     = google_vertex_ai_endpoint.serving[0].display_name == "test-serving-endpoint"
    error_message = "endpoint display_name must be var.project-prefixed."
  }

  # location (the required field) is fed from var.region — pin the flow-through
  # so a swap to the wrong source is caught.
  assert {
    condition     = google_vertex_ai_endpoint.serving[0].location == "us-central1"
    error_message = "endpoint location must be fed from var.region (default us-central1)."
  }

  # endpoint_name output mirrors the resource name on the serving path (and is
  # null on the off path — see serving_off_by_default-adjacent coverage).
  assert {
    condition     = output.endpoint_name == "2676412545"
    error_message = "endpoint_name output must surface the numeric endpoint name when serving is on."
  }

  # Vector Search resources are untouched by the serving flag.
  assert {
    condition     = length(google_vertex_ai_index.this) == 0
    error_message = "enable_serving must not create Vector Search resources (orthogonal flags)."
  }

  # Dataset still composes.
  assert {
    condition     = google_vertex_ai_dataset.dataset.display_name == "main-dataset"
    error_message = "dataset must still be created when only serving is enabled."
  }
}

run "model_garden" {
  command = plan

  variables {
    project            = "test"
    project_id         = "test-project"
    enable_serving     = true
    model_garden_model = "publishers/google/models/gemma3@gemma-3-1b-it"
    # model_garden_accept_eula left at its FALSE default on purpose: asserting
    # accept_eula == false below pins the variable->attribute edge in the
    # direction a hardcoded `accept_eula = true` would otherwise survive. The
    # EULA-true flow-through is exercised in model_garden_with_accelerator.
  }

  # Both the bare endpoint AND the model-garden deployment compose.
  assert {
    condition     = length(google_vertex_ai_endpoint.serving) == 1
    error_message = "model-garden path still creates the bare serving endpoint."
  }

  assert {
    condition     = length(google_vertex_ai_endpoint_with_model_garden_deployment.model_garden) == 1
    error_message = "enable_serving + model_garden_model must create exactly one model-garden deployment."
  }

  # The publisher model name flows through verbatim.
  assert {
    condition     = google_vertex_ai_endpoint_with_model_garden_deployment.model_garden[0].publisher_model_name == "publishers/google/models/gemma3@gemma-3-1b-it"
    error_message = "model_garden_model must reach publisher_model_name verbatim."
  }

  # EULA default (false) reaches model_config.accept_eula. Catches a hardcoded
  # `accept_eula = true` (consent must not be silently granted).
  assert {
    condition     = google_vertex_ai_endpoint_with_model_garden_deployment.model_garden[0].model_config[0].accept_eula == false
    error_message = "model_garden_accept_eula default (false) must reach model_config.accept_eula — EULA consent must not be hardcoded."
  }

  # CPU-only default: machine_type set, accelerator fields null.
  assert {
    condition     = google_vertex_ai_endpoint_with_model_garden_deployment.model_garden[0].deploy_config[0].dedicated_resources[0].machine_spec[0].machine_type == "n1-standard-4"
    error_message = "serving_machine_type default (n1-standard-4) must reach machine_spec.machine_type."
  }

  assert {
    condition     = google_vertex_ai_endpoint_with_model_garden_deployment.model_garden[0].deploy_config[0].dedicated_resources[0].machine_spec[0].accelerator_type == null
    error_message = "CPU-only default must leave machine_spec.accelerator_type null (no GPU quota demanded)."
  }

  # Generous create timeout for the long-running model deploy.
  assert {
    condition     = google_vertex_ai_endpoint_with_model_garden_deployment.model_garden[0].timeouts.create == "60m"
    error_message = "model-garden deployment must carry a generous create timeout (deploys run 30+ min)."
  }
}

run "model_garden_with_accelerator" {
  command = plan

  variables {
    project                   = "test"
    project_id                = "test-project"
    enable_serving            = true
    model_garden_model        = "publishers/google/models/llama3-2@llama-3.2-1b"
    model_garden_accept_eula  = true
    serving_machine_type      = "g2-standard-16"
    serving_accelerator_type  = "NVIDIA_L4"
    serving_accelerator_count = 1
  }

  # GPU path: accelerator type + count flow through to machine_spec.
  assert {
    condition     = google_vertex_ai_endpoint_with_model_garden_deployment.model_garden[0].deploy_config[0].dedicated_resources[0].machine_spec[0].accelerator_type == "NVIDIA_L4"
    error_message = "serving_accelerator_type must reach machine_spec.accelerator_type when set."
  }

  assert {
    condition     = google_vertex_ai_endpoint_with_model_garden_deployment.model_garden[0].deploy_config[0].dedicated_resources[0].machine_spec[0].accelerator_count == 1
    error_message = "serving_accelerator_count must reach machine_spec.accelerator_count when an accelerator type is set."
  }

  # EULA acceptance flows through when set true (the catch-the-hardcoded-false
  # direction, paired with the accept_eula == false assert in run "model_garden").
  assert {
    condition     = google_vertex_ai_endpoint_with_model_garden_deployment.model_garden[0].model_config[0].accept_eula == true
    error_message = "model_garden_accept_eula=true must reach model_config.accept_eula."
  }
}

run "serving_alarms_emitted_when_serving_and_observability_on" {
  command = plan

  variables {
    project              = "test"
    project_id           = "test-project"
    enable_serving       = true
    enable_observability = true
  }

  assert {
    condition     = length(google_monitoring_alert_policy.serving_prediction_latency_high) == 1
    error_message = "prediction-latency alarm must be emitted when serving AND observability are both on."
  }

  assert {
    condition     = length(google_monitoring_alert_policy.serving_prediction_errors_high) == 1
    error_message = "prediction-error alarm must be emitted when serving AND observability are both on."
  }

  # Filters must carry the metric.types verified against Google's official list
  # and registered in the pkg/observability vertexai catalog.
  assert {
    condition = strcontains(
      google_monitoring_alert_policy.serving_prediction_latency_high["0"].conditions[0].condition_threshold[0].filter,
      "metric.type=\"aiplatform.googleapis.com/prediction/online/prediction_latencies\""
    )
    error_message = "latency alarm filter must reference prediction/online/prediction_latencies."
  }

  assert {
    condition = strcontains(
      google_monitoring_alert_policy.serving_prediction_errors_high["0"].conditions[0].condition_threshold[0].filter,
      "metric.type=\"aiplatform.googleapis.com/prediction/online/error_count\""
    )
    error_message = "error alarm filter must reference prediction/online/error_count."
  }
}

run "serving_alarms_absent_when_serving_off" {
  command = plan

  variables {
    project              = "test"
    project_id           = "test-project"
    enable_serving       = false
    enable_observability = true
    # Vector Search on so observability is clearly active for the vector alarm,
    # proving the serving alarms gate on enable_serving specifically.
    enable_vector_search = true
  }

  assert {
    condition     = length(google_monitoring_alert_policy.serving_prediction_latency_high) == 0
    error_message = "serving alarms must gate on enable_serving, not enable_vector_search."
  }

  assert {
    condition     = length(google_monitoring_alert_policy.serving_prediction_errors_high) == 0
    error_message = "serving error alarm must be absent when serving is off."
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

run "rejects_empty_network" {
  command = plan

  variables {
    project    = "test"
    project_id = "test-project"
    network    = "" # would parse to an empty network name with no signal
  }

  expect_failures = [var.network]
}

run "rejects_malformed_network_path" {
  command = plan

  variables {
    project    = "test"
    project_id = "test-project"
    # Trailing slash -> last-segment parse would yield "" ; not a valid bare
    # name nor the exact projects/.../networks/<name> form.
    network = "projects/test-project/global/networks/"
  }

  expect_failures = [var.network]
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

run "rejects_bad_model_garden_format" {
  command = plan

  variables {
    project        = "test"
    project_id     = "test-project"
    enable_serving = true
    # Bare model name, not the publishers/.../models/...@version form.
    model_garden_model = "gemma-3-1b-it"
  }

  expect_failures = [var.model_garden_model]
}

run "rejects_model_garden_missing_version" {
  command = plan

  variables {
    project        = "test"
    project_id     = "test-project"
    enable_serving = true
    # Missing the @<version> suffix the API requires.
    model_garden_model = "publishers/google/models/gemma3"
  }

  expect_failures = [var.model_garden_model]
}

run "rejects_serving_max_replicas_below_min" {
  command = plan

  variables {
    project              = "test"
    project_id           = "test-project"
    enable_serving       = true
    model_garden_model   = "publishers/google/models/gemma3@gemma-3-1b-it"
    serving_min_replicas = 3
    serving_max_replicas = 1 # ceiling below floor
  }

  # Cross-variable invariant is a precondition on the deployment resource.
  expect_failures = [google_vertex_ai_endpoint_with_model_garden_deployment.model_garden]
}

run "rejects_accelerator_type_without_count" {
  command = plan

  variables {
    project                  = "test"
    project_id               = "test-project"
    enable_serving           = true
    model_garden_model       = "publishers/google/models/gemma3@gemma-3-1b-it"
    serving_accelerator_type = "NVIDIA_L4"
    # count left at its 0 default -> half-configured GPU request.
  }

  expect_failures = [google_vertex_ai_endpoint_with_model_garden_deployment.model_garden]
}

run "rejects_accelerator_count_without_type" {
  command = plan

  variables {
    project                   = "test"
    project_id                = "test-project"
    enable_serving            = true
    model_garden_model        = "publishers/google/models/gemma3@gemma-3-1b-it"
    serving_accelerator_count = 2
    # type left null -> half-configured GPU request.
  }

  expect_failures = [google_vertex_ai_endpoint_with_model_garden_deployment.model_garden]
}

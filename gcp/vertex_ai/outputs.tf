output "dataset_id" {
  value       = google_vertex_ai_dataset.dataset.id
  description = "The resource ID of the Vertex AI dataset"
}

output "dataset_name" {
  value       = google_vertex_ai_dataset.dataset.display_name
  description = "The display name of the Vertex AI dataset"
}

output "dataset_resource_name" {
  value       = google_vertex_ai_dataset.dataset.name
  description = "The full resource name of the Vertex AI dataset (projects/<n>/locations/<r>/datasets/<id>)"
}

output "region" {
  value       = google_vertex_ai_dataset.dataset.region
  description = "The region of the Vertex AI dataset"
}

# --- Vector Search ----------------------------------------------------------
# Null when var.enable_vector_search is false (the resources are not created).

output "index_id" {
  value       = var.enable_vector_search ? google_vertex_ai_index.this[0].id : null
  description = "The resource ID of the Vector Search index (null when Vector Search is disabled)"
}

output "index_endpoint_id" {
  value       = var.enable_vector_search ? google_vertex_ai_index_endpoint.this[0].id : null
  description = "The resource ID of the Vector Search index endpoint (null when Vector Search is disabled)"
}

output "deployed_index_id" {
  value       = var.enable_vector_search ? google_vertex_ai_index_endpoint_deployed_index.this[0].deployed_index_id : null
  description = "The deployed-index ID binding the index to the endpoint (null when Vector Search is disabled)"
}

# --- Model serving (#768) ---------------------------------------------------
# Null when var.enable_serving is false (no endpoint is created).
#
# The bare endpoint and the Model Garden deployment are mutually exclusive (see
# main.tf): exactly one exists when serving is on. The outputs return whichever
# one that is, so they never point at an empty shell:
#   - bare endpoint path (no model named): google_vertex_ai_endpoint.serving
#   - model-garden path (model named):     google_vertex_ai_endpoint_with_model_garden_deployment.model_garden
#       .id       -> full resource name projects/<p>/locations/<l>/endpoints/<e>
#       .endpoint -> the bare endpoint ID segment (matches the bare endpoint's .name)

output "endpoint_id" {
  value = (
    local.model_garden_enabled ? google_vertex_ai_endpoint_with_model_garden_deployment.model_garden[0].id :
    local.bare_endpoint_enabled ? google_vertex_ai_endpoint.serving[0].id :
    null
  )
  description = "The full resource name of the Vertex AI serving endpoint — the Model Garden deployment's own endpoint when a model is deployed, otherwise the bare endpoint (null when serving is disabled)"
}

output "endpoint_name" {
  value = (
    local.model_garden_enabled ? google_vertex_ai_endpoint_with_model_garden_deployment.model_garden[0].endpoint :
    local.bare_endpoint_enabled ? google_vertex_ai_endpoint.serving[0].name :
    null
  )
  description = "The numeric endpoint ID segment of the Vertex AI serving endpoint — the Model Garden deployment's endpoint when a model is deployed, otherwise the bare endpoint (null when serving is disabled)"
}

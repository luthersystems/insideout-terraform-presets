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
# Null when var.enable_serving is false (the endpoint is not created).

output "endpoint_id" {
  value       = var.enable_serving ? google_vertex_ai_endpoint.serving[0].id : null
  description = "The resource ID of the Vertex AI serving endpoint (null when serving is disabled)"
}

output "endpoint_name" {
  value       = var.enable_serving ? google_vertex_ai_endpoint.serving[0].name : null
  description = "The numeric resource name of the Vertex AI serving endpoint (null when serving is disabled)"
}

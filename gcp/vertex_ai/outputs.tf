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

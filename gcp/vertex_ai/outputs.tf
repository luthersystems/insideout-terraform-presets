output "dataset_id" {
  value       = google_vertex_ai_dataset.dataset.id
  description = "The ID of the Vertex AI dataset"
}

output "dataset_name" {
  value       = google_vertex_ai_dataset.dataset.display_name
  description = "The display name of the Vertex AI dataset"
}

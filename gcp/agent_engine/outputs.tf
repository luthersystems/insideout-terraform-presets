output "reasoning_engine_id" {
  description = "The resource ID of the Vertex AI Reasoning Engine."
  value       = google_vertex_ai_reasoning_engine.this.id
}

output "reasoning_engine_name" {
  description = "The full generated resource name of the Reasoning Engine (projects/<project>/locations/<region>/reasoningEngines/<id>)."
  value       = google_vertex_ai_reasoning_engine.this.name
}

output "reasoning_engine_display_name" {
  description = "The display name of the Reasoning Engine."
  value       = google_vertex_ai_reasoning_engine.this.display_name
}

output "region" {
  description = "The region the Reasoning Engine is deployed in."
  value       = google_vertex_ai_reasoning_engine.this.region
}

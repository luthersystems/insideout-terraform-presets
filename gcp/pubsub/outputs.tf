output "topic_id" {
  description = "The ID of the Pub/Sub topic"
  value       = google_pubsub_topic.this.id
}

output "topic_name" {
  description = "The name of the Pub/Sub topic"
  value       = google_pubsub_topic.this.name
}

output "subscription_id" {
  description = "The ID of the Pub/Sub subscription"
  value       = google_pubsub_subscription.this.id
}

output "subscription_name" {
  description = "The name of the Pub/Sub subscription"
  value       = google_pubsub_subscription.this.name
}

output "dead_letter_topic_id" {
  description = "The ID of the dead letter topic (if enabled)"
  value       = var.enable_dead_letter ? google_pubsub_topic.dead_letter[0].id : null
}

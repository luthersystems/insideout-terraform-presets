output "bucket_name" {
  description = "The bucket name"
  value       = google_storage_bucket.this.name
}

output "bucket_id" {
  description = "The bucket ID"
  value       = google_storage_bucket.this.id
}

output "bucket_self_link" {
  description = "The bucket self link"
  value       = google_storage_bucket.this.self_link
}

output "bucket_url" {
  description = "The bucket URL"
  value       = google_storage_bucket.this.url
}

output "bucket_arn" {
  description = "The bucket ARN-style identifier (for compatibility)"
  value       = "gs://${google_storage_bucket.this.name}"
}

output "location" {
  description = "The bucket location"
  value       = google_storage_bucket.this.location
}

output "storage_class" {
  description = "The bucket storage class"
  value       = google_storage_bucket.this.storage_class
}


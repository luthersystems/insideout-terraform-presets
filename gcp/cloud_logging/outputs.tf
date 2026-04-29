output "sink_name" {
  value       = google_logging_project_sink.sink.name
  description = "The name of the logging sink"
}

output "sink_writer_identity" {
  value       = google_logging_project_sink.sink.writer_identity
  description = "The writer identity of the logging sink for granting destination permissions"
}

output "logs_bucket_name" {
  value       = google_storage_bucket.logs.name
  description = "The GCS bucket that holds archived logs."
}

output "logs_bucket_url" {
  value       = google_storage_bucket.logs.url
  description = "The gs:// URL for the logs bucket."
}

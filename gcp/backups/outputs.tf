output "backup_bucket_name" {
  description = "Name of the backup GCS bucket"
  value       = var.enable_gcs_backups ? google_storage_bucket.backups[0].name : null
}

output "backup_bucket_url" {
  description = "URL of the backup GCS bucket"
  value       = var.enable_gcs_backups ? google_storage_bucket.backups[0].url : null
}

output "snapshot_schedule_id" {
  description = "ID of the compute snapshot schedule"
  value       = var.enable_compute_snapshots ? google_compute_resource_policy.snapshot_schedule[0].id : null
}

output "snapshot_schedule_name" {
  description = "Name of the compute snapshot schedule"
  value       = var.enable_compute_snapshots ? google_compute_resource_policy.snapshot_schedule[0].name : null
}

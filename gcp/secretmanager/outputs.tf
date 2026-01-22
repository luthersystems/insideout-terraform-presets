output "secret_ids" {
  description = "Map of secret names to secret IDs"
  value       = { for k, v in google_secret_manager_secret.this : k => v.id }
}

output "secret_names" {
  description = "Map of logical names to full secret names"
  value       = { for k, v in google_secret_manager_secret.this : k => v.secret_id }
}

output "secret_version_ids" {
  description = "Map of secret names to latest version IDs"
  value       = { for k, v in google_secret_manager_secret_version.this : k => v.id }
}

output "secret_version_names" {
  description = "Map of secret names to version names (for accessing)"
  value       = { for k, v in google_secret_manager_secret_version.this : k => v.name }
}


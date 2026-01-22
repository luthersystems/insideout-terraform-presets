output "config_name" {
  description = "The name of the Identity Platform config"
  value       = google_identity_platform_config.this.name
}

output "authorized_domains" {
  description = "List of authorized domains"
  value       = google_identity_platform_config.this.authorized_domains
}

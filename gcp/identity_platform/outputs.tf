output "config_name" {
  description = "Canonical name of the Identity Platform config. Stable for both created and adopted configs since the API path is deterministic per project."
  value       = "projects/${var.project_id}/config"
}

output "authorized_domains" {
  description = "List of authorized domains. Populated only when this module CREATEd the config (greenfield); null when the config was adopted. Callers needing this on adopted projects must query the IP REST API directly."
  value       = try(google_identity_platform_config.this[0].authorized_domains, null)
}

output "adopted" {
  description = "True if this module skipped CREATE because the Identity Platform config already existed on the project (existence probe returned 200)."
  value       = !local.ip_should_create
}

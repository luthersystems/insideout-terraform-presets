output "external_ip" {
  description = "The external IP address"
  value       = google_compute_global_address.this.address
}

output "external_ip_name" {
  description = "The external IP address name"
  value       = google_compute_global_address.this.name
}

output "url_map_id" {
  description = "The URL map ID"
  value       = google_compute_url_map.this.id
}

output "url_map_self_link" {
  description = "The URL map self link"
  value       = google_compute_url_map.this.self_link
}

output "http_proxy_id" {
  description = "The HTTP proxy ID"
  value       = google_compute_target_http_proxy.this.id
}

output "https_proxy_id" {
  description = "The HTTPS proxy ID (if SSL enabled)"
  value       = var.enable_ssl ? google_compute_target_https_proxy.this[0].id : null
}

output "backend_service_ids" {
  description = "Map of backend service names to IDs"
  value       = { for k, v in google_compute_backend_service.this : k => v.id }
}

output "health_check_ids" {
  description = "Map of health check names to IDs"
  value       = { for k, v in google_compute_health_check.this : k => v.id }
}

output "ssl_certificate_id" {
  description = "The managed SSL certificate ID (if created)"
  value       = length(var.managed_ssl_domains) > 0 ? google_compute_managed_ssl_certificate.this[0].id : null
}


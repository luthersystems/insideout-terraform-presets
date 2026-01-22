output "api_id" {
  description = "The ID of the API"
  value       = google_api_gateway_api.this.api_id
}

output "gateway_id" {
  description = "The ID of the gateway"
  value       = google_api_gateway_gateway.this.gateway_id
}

output "gateway_url" {
  description = "The default URL for the gateway"
  value       = google_api_gateway_gateway.this.default_hostname
}

output "managed_service" {
  description = "The managed service name"
  value       = google_api_gateway_api.this.managed_service
}

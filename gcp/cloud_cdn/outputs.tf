output "cdn_enabled" {
  description = "Whether Cloud CDN is enabled"
  value       = true
}

output "cache_mode" {
  description = "Cache mode used"
  value       = var.cache_mode
}

output "default_ttl" {
  description = "Default TTL for cached content"
  value       = var.default_ttl
}

output "keyring_id" {
  description = "The key ring ID"
  value       = google_kms_key_ring.this.id
}

output "keyring_name" {
  description = "The key ring name"
  value       = google_kms_key_ring.this.name
}

output "keyring_self_link" {
  description = "The key ring self link"
  value       = google_kms_key_ring.this.id
}

output "keys" {
  description = "Map of key names to key IDs"
  value       = local.keys_by_name
}

output "key_self_links" {
  description = "Map of key names to self links"
  value       = { for k, v in local.keys_by_name : k => v }
}

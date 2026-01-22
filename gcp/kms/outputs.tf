output "keyring_id" {
  description = "The key ring ID"
  value       = module.kms.keyring
}

output "keyring_name" {
  description = "The key ring name"
  value       = module.kms.keyring_name
}

output "keyring_self_link" {
  description = "The key ring self link"
  value       = module.kms.keyring_resource.id
}

output "keys" {
  description = "Map of key names to key IDs"
  value       = module.kms.keys
}

output "key_self_links" {
  description = "Map of key names to self links"
  value       = { for k, v in module.kms.keys : k => v }
}


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
  # Routed through the local that wraps the upstream's keys_by_name in
  # try() (issue #180). On the happy path this is the upstream value;
  # if the upstream's slice() ever errors during plan against an empty
  # state, consumers see an empty map and skip rather than failing.
  value = local.keys_by_name
}

output "key_self_links" {
  description = "Map of key names to self links"
  value       = { for k, v in local.keys_by_name : k => v }
}


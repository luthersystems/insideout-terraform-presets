output "zone_id" {
  description = "Cloud DNS managed zone fully-qualified ID (works for both create_zone=true and false). Wire into downstream modules that need to attach records out-of-band or build zone references."
  value       = local.zone_id
}

output "zone_name" {
  description = "Cloud DNS managed zone resource name (e.g. \"example-com\"). Useful as the `managed_zone` argument on out-of-band record sets and for cross-module wiring."
  value       = local.zone_name
}

output "dns_name" {
  description = "Fully-qualified DNS name for the zone (always trailing-dot, e.g. \"example.com.\"). Lets downstream callers reference the zone's apex without reparsing var.dns_name."
  value       = local.dns_name
}

output "name_servers" {
  description = "Authoritative name servers for the managed zone. Required for delegating a domain to Cloud DNS when create_zone = true and the registrar lives elsewhere. Empty list when create_zone = false (the data source exposes name_servers, but we keep parity with the AWS module's contract of \"set only on create\")."
  value       = var.create_zone ? google_dns_managed_zone.this[0].name_servers : []
}

output "record_fqdns" {
  description = "Map of record-key (\"<name>-<type>\") -> resolved FQDN, covering all record sets managed by this module. Lets downstream callers reference the record without rebuilding the FQDN."
  value       = { for k, r in google_dns_record_set.records : k => r.name }
}

output "record_names" {
  description = "Sorted list of all record short-names managed by this module. Useful for assertions / discovery."
  value       = sort([for r in var.records : r.name])
}

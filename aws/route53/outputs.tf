output "zone_id" {
  description = "Route 53 hosted zone ID (works for both create_zone=true and false). Wire into downstream modules that need to attach records or build alias targets."
  value       = local.zone_id
}

output "zone_arn" {
  description = "ARN of the hosted zone (null when create_zone=false; the data source does not expose the ARN). Use this for IAM policies targeting the zone."
  value       = var.create_zone ? aws_route53_zone.this[0].arn : null
}

output "zone_name" {
  description = "Fully-qualified hosted zone name (e.g. example.com.). Useful when callers need to build FQDNs without reparsing var.domain_name."
  value       = local.zone_name
}

output "name_servers" {
  description = "Authoritative name servers for the hosted zone. Required for delegating a domain to Route 53 when create_zone = true and the registrar lives elsewhere."
  value       = var.create_zone ? aws_route53_zone.this[0].name_servers : []
}

output "record_fqdns" {
  description = "Map of record-key (\"<name>-<type>\") -> resolved FQDN, covering both plain records and aliases. Lets downstream callers reference the record without rebuilding the FQDN."
  value = merge(
    { for k, r in aws_route53_record.records : k => r.fqdn },
    { for k, r in aws_route53_record.aliases : k => r.fqdn },
  )
}

output "record_names" {
  description = "List of all record short-names managed by this module (across both plain and alias records). Useful for assertions / discovery."
  value = sort(concat(
    [for r in aws_route53_record.records : r.name],
    [for r in aws_route53_record.aliases : r.name],
  ))
}

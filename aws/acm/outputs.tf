output "certificate_arn" {
  description = "ACM certificate ARN. Wire into ALB listener (`certificate_arn`), API Gateway domain name (`regional_certificate_arn` / `certificate_arn`), or CloudFront viewer certificate (`acm_certificate_arn`). When create_validation = true this resolves only after the cert is ISSUED."
  value       = var.create_validation ? aws_acm_certificate_validation.this[0].certificate_arn : aws_acm_certificate.this.arn
}

output "certificate_id" {
  description = "ACM certificate ID (the trailing UUID of the ARN). Useful for IAM resource references that don't accept ARNs."
  value       = aws_acm_certificate.this.id
}

output "domain_name" {
  description = "Primary domain name on the certificate. Echoed for downstream wiring convenience so callers don't need to repeat the input."
  value       = aws_acm_certificate.this.domain_name
}

output "validation_records" {
  description = <<-EOT
    DNS validation records the caller must publish for ACM to issue the cert.
    One entry per unique validation record (primary + SANs). Each entry is a
    map with `name`, `type`, and `value` — ready to feed into
    `aws/route53.records` (one record per entry, with `ttl = 60`
    recommended). Deduplicated by `resource_record_name` so wildcard + apex
    covering the same zone don't produce two identical records.
  EOT
  # ACM emits one domain_validation_options entry per requested domain
  # (primary + each SAN). When a wildcard like *.example.com is paired with
  # an apex example.com SAN, ACM returns the same _acme-challenge.example.com
  # record for both — dedupe so the caller doesn't try to create the same
  # Route 53 record twice.
  value = values({
    for opt in aws_acm_certificate.this.domain_validation_options :
    opt.resource_record_name => {
      name  = opt.resource_record_name
      type  = opt.resource_record_type
      value = opt.resource_record_value
    }
  })
}

output "validation_record_fqdns" {
  description = "Plain list of FQDNs from `validation_records`, for callers that just need the names to pass back into `var.validation_record_fqdns` on a second-pass apply."
  value       = distinct([for opt in aws_acm_certificate.this.domain_validation_options : opt.resource_record_name])
}

output "domain_validation_options" {
  description = "Raw `domain_validation_options` set from `aws_acm_certificate`. Prefer `validation_records` for typical wiring; this raw output exists for callers that need extra fields (e.g. per-domain validation status)."
  value       = aws_acm_certificate.this.domain_validation_options
}

output "status" {
  description = "Current ACM certificate status (PENDING_VALIDATION, ISSUED, etc.). Useful for assertions / reporting; not load-bearing for wiring."
  value       = aws_acm_certificate.this.status
}

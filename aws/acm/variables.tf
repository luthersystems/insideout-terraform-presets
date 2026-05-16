variable "region" {
  description = "AWS region the certificate is provisioned in. For CloudFront use pin to us-east-1; for ALB / API Gateway match the consumer's region."
  type        = string

  validation {
    condition     = length(trimspace(var.region)) > 0
    error_message = "region must be a non-empty string."
  }
}

variable "project" {
  description = "Project name prefix used for tagging and naming."
  type        = string

  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{1,61}[a-z0-9]$", var.project))
    error_message = "project must be lowercase alphanumeric with hyphens, 3-63 characters."
  }
}

variable "environment" {
  description = "Deployment environment (e.g. production, staging, sandbox)."
  type        = string

  validation {
    condition     = length(trimspace(var.environment)) > 0
    error_message = "environment must be a non-empty string."
  }
}

variable "domain_name" {
  description = "Primary fully-qualified domain name the certificate is issued for (e.g. www.example.com or *.example.com)."
  type        = string

  validation {
    condition     = length(trimspace(var.domain_name)) > 0
    error_message = "domain_name must be a non-empty string."
  }

  # Allows wildcards (leading "*.") and standard RFC-1035 labels.
  validation {
    condition     = can(regex("^(\\*\\.)?[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(\\.[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)+$", var.domain_name))
    error_message = "domain_name must be a syntactically valid DNS name (RFC 1035 labels), optionally prefixed with '*.' for a wildcard."
  }
}

variable "subject_alternative_names" {
  description = "Additional FQDNs (SANs) the certificate covers. Each entry must be a syntactically valid DNS name; wildcards (leading '*.') are allowed."
  type        = list(string)
  default     = []

  validation {
    condition = alltrue([
      for s in var.subject_alternative_names :
      can(regex("^(\\*\\.)?[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(\\.[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)+$", s))
    ])
    error_message = "Each subject_alternative_names entry must be a syntactically valid DNS name (optionally wildcarded)."
  }

  # ACM caps a public cert at 10 SANs (including the primary domain). Reject
  # over-spec at plan time rather than waiting for an AWS-side rejection.
  validation {
    condition     = length(var.subject_alternative_names) <= 9
    error_message = "subject_alternative_names supports up to 9 entries (ACM caps a public cert at 10 names total including the primary domain_name)."
  }
}

variable "key_algorithm" {
  description = "Key algorithm for the certificate. RSA_2048 is the broadest-compatibility default; ECDSA variants trade compatibility for smaller TLS handshakes."
  type        = string
  default     = "RSA_2048"

  validation {
    condition     = contains(["RSA_2048", "EC_prime256v1", "EC_secp384r1"], var.key_algorithm)
    error_message = "key_algorithm must be one of RSA_2048, EC_prime256v1, EC_secp384r1."
  }
}

variable "certificate_transparency_logging" {
  description = "Certificate Transparency logging preference. Public web certs should keep this ENABLED (browser/CT-log requirement); DISABLED is only meaningful for internal-only PKI use cases with a documented exception."
  type        = string
  default     = "ENABLED"

  validation {
    condition     = contains(["ENABLED", "DISABLED"], var.certificate_transparency_logging)
    error_message = "certificate_transparency_logging must be ENABLED or DISABLED."
  }
}

variable "create_validation" {
  description = <<-EOT
    If true, create an `aws_acm_certificate_validation` that blocks the apply
    until AWS confirms the cert is ISSUED. Requires the DNS validation
    records (see the `validation_records` output) to already be present in
    DNS — typically by feeding `validation_records` into `aws/route53.records`
    and `validation_record_fqdns` back into this module.

    Set false when the DNS provider is external/unmanaged or when accepting a
    PENDING_VALIDATION cert in state on first apply.
  EOT
  type        = bool
  default     = false
}

variable "validation_record_fqdns" {
  description = "List of FQDNs that the DNS validation records were written under. Only consulted when create_validation = true. Caller supplies these after creating the records in DNS (typically derived from `aws/route53.record_fqdns`)."
  type        = list(string)
  default     = []

  validation {
    condition = alltrue([
      for f in var.validation_record_fqdns : length(trimspace(f)) > 0
    ])
    error_message = "Each validation_record_fqdns entry must be a non-empty string."
  }
}

variable "validation_timeout" {
  description = "Maximum time to wait for AWS to mark the cert ISSUED when create_validation = true. ACM DNS validation typically completes in a few minutes but the published SLA is up to 72 hours; '45m' is a pragmatic ceiling for stack deploys."
  type        = string
  default     = "45m"

  validation {
    condition     = can(regex("^[0-9]+[smh]$", var.validation_timeout))
    error_message = "validation_timeout must be a Go duration string (e.g. '30m', '1h', '90s')."
  }
}

variable "tags" {
  description = "Common resource tags. Applied to the ACM certificate (the only taggable resource in this module)."
  type        = map(string)
  default     = {}
}

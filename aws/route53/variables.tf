variable "region" {
  description = "AWS region (e.g., us-east-1). Route 53 itself is a global service, but the region is required for the provider and for module-name composition."
  type        = string
}

variable "project" {
  description = "Project name prefix used for tagging and naming"
  type        = string

  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{1,61}[a-z0-9]$", var.project))
    error_message = "project must be lowercase alphanumeric with hyphens, 3-63 characters."
  }
}

variable "environment" {
  description = "Deployment environment (e.g. production, staging, sandbox)"
  type        = string

  validation {
    condition     = length(trimspace(var.environment)) > 0
    error_message = "environment must be a non-empty string."
  }
}

variable "domain_name" {
  description = "Apex domain (e.g. example.com) for the hosted zone."
  type        = string

  validation {
    condition     = length(trimspace(var.domain_name)) > 0
    error_message = "domain_name must be a non-empty string."
  }

  # Public hosted zones (and most private ones) require a syntactically
  # valid DNS name. Allows trailing dot per RFC 1035 § 5.1; Route 53
  # accepts either.
  validation {
    condition     = can(regex("^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(\\.[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*\\.?$", var.domain_name))
    error_message = "domain_name must be a syntactically valid DNS domain (RFC 1035 labels separated by dots)."
  }
}

variable "create_zone" {
  description = "If true, create a new Route 53 hosted zone for var.domain_name. If false, look up an existing zone via var.zone_id."
  type        = bool
  default     = false
}

variable "zone_id" {
  description = "Existing Route 53 hosted zone ID. Required when create_zone = false; ignored when create_zone = true."
  type        = string
  default     = null

  # Null-safe per Terraform's lack of short-circuit on ||.
  validation {
    condition     = var.zone_id == null ? true : can(regex("^Z[A-Z0-9]{1,31}$", var.zone_id))
    error_message = "zone_id must look like a Route 53 zone ID (e.g. Z2FDTNDATAQYW2)."
  }
}

variable "private_zone" {
  description = "If true, the (created or queried) hosted zone is private. Private zones must be associated with at least one VPC."
  type        = bool
  default     = false
}

variable "vpc_ids" {
  description = "VPC IDs to associate with a private hosted zone. Required when private_zone = true and create_zone = true; ignored otherwise."
  type        = list(string)
  default     = []
}

variable "force_destroy" {
  description = "Allow `terraform destroy` to delete a created hosted zone even when it still contains record sets. Has no effect when create_zone = false."
  type        = bool
  default     = false
}

variable "records" {
  description = <<-EOT
    Plain record sets — CNAME / A / AAAA / TXT / MX / SRV / NS / PTR / SPF.
    Each entry creates one `aws_route53_record` with an explicit TTL. Use
    an empty `name` ("") to target the apex.

    Example:
      records = [
        { name = "www",  type = "CNAME", ttl = 300, values = ["target.example.com"] },
        { name = "",     type = "TXT",   ttl = 300, values = ["\"v=spf1 -all\""] },
        { name = "mail", type = "MX",    ttl = 300, values = ["10 inbound.example.com"] },
      ]
  EOT
  type = list(object({
    name   = string
    type   = string
    ttl    = number
    values = list(string)
  }))
  default = []

  validation {
    condition = alltrue([
      for r in var.records :
      contains(["A", "AAAA", "CNAME", "MX", "NS", "PTR", "SOA", "SPF", "SRV", "TXT", "CAA", "DS", "NAPTR"], r.type)
    ])
    error_message = "Each record.type must be one of A, AAAA, CNAME, MX, NS, PTR, SOA, SPF, SRV, TXT, CAA, DS, NAPTR. Use the `aliases` variable for alias records."
  }

  validation {
    condition     = alltrue([for r in var.records : r.ttl >= 0 && r.ttl <= 2147483647])
    error_message = "Each record.ttl must be a non-negative 32-bit integer (Route 53 max TTL is 2147483647 seconds)."
  }

  validation {
    condition     = alltrue([for r in var.records : length(r.values) > 0])
    error_message = "Each record must have at least one entry in `values`."
  }
}

variable "aliases" {
  description = <<-EOT
    Alias records pointing at an AWS service endpoint (ALB, CloudFront,
    API Gateway, S3 website, etc.). Alias records have no TTL — caching
    is governed by the target. `type` defaults to "A"; set to "AAAA" only
    for IPv6 alias targets. `evaluate_target_health` defaults to false
    and must remain false for CloudFront / API Gateway targets.

    Example:
      aliases = [
        {
          name                   = ""          # apex
          target_dns_name        = module.alb.alb_dns_name
          target_zone_id         = module.alb.alb_zone_id
          evaluate_target_health = true
        },
        {
          name                   = "cdn"
          target_dns_name        = module.cloudfront.domain_name
          target_zone_id         = "Z2FDTNDATAQYW2"  # CloudFront's static zone
        },
      ]
  EOT
  type = list(object({
    name                   = string
    target_dns_name        = string
    target_zone_id         = string
    type                   = optional(string, "A")
    evaluate_target_health = optional(bool, false)
  }))
  default = []

  validation {
    condition     = alltrue([for a in var.aliases : contains(["A", "AAAA"], a.type)])
    error_message = "alias.type must be A or AAAA (Route 53 alias records are restricted to these two types)."
  }

  validation {
    condition     = alltrue([for a in var.aliases : length(trimspace(a.target_dns_name)) > 0])
    error_message = "Each alias must specify a non-empty target_dns_name."
  }

  validation {
    condition     = alltrue([for a in var.aliases : can(regex("^Z[A-Z0-9]{1,31}$", a.target_zone_id))])
    error_message = "Each alias.target_zone_id must look like a Route 53 hosted-zone ID (e.g. Z2FDTNDATAQYW2 for CloudFront)."
  }
}

variable "tags" {
  description = "Common resource tags. Applied to the hosted zone (the only taggable Route 53 resource in this module)."
  type        = map(string)
  default     = {}
}

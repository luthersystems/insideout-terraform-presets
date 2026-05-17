variable "project" {
  description = "Naming/label prefix for stack resources (NOT a GCP project ID — see var.project_id)."
  type        = string

  validation {
    condition     = length(trimspace(var.project)) > 0
    error_message = "project must be a non-empty string."
  }
}

variable "project_id" {
  description = "Real GCP project ID where the managed zone and record sets are created (e.g. \"my-prod-12345\"). Distinct from var.project, which is the naming/label prefix and need not be a valid GCP project ID."
  type        = string

  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{4,28}[a-z0-9]$", var.project_id))
    error_message = "project_id must be a valid GCP project ID: 6–30 chars, lowercase letters/digits/hyphens, start with a letter, end alphanumeric."
  }
}

# tflint-ignore: terraform_unused_declarations  # composer always wires var.region at the root (CLAUDE.md mandate). Cloud DNS itself is global, but the composer namespacing convention requires region.
variable "region" {
  description = "GCP region. Cloud DNS is a global service, but the region is required for the provider and for composer namespacing parity with other modules."
  type        = string
  default     = "us-central1"
}

variable "dns_name" {
  description = "Apex DNS name for the managed zone (e.g. \"example.com.\"). Cloud DNS requires a trailing dot — a trailing dot is appended if missing in the create-zone path."
  type        = string

  validation {
    condition     = length(trimspace(var.dns_name)) > 0
    error_message = "dns_name must be a non-empty string."
  }

  # Cloud DNS DNS names must be syntactically valid; allow optional
  # trailing dot per RFC 1035 § 3.1. Cloud DNS itself requires the dot
  # at apply time, but we accept it either way for caller ergonomics.
  validation {
    condition     = can(regex("^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(\\.[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*\\.?$", var.dns_name))
    error_message = "dns_name must be a syntactically valid DNS domain (RFC 1035 labels separated by dots, optional trailing dot)."
  }
}

variable "create_zone" {
  description = "If true, create a new Cloud DNS managed zone for var.dns_name. If false, look up an existing zone via var.zone_name."
  type        = bool
  default     = false
}

variable "zone_short_name" {
  description = "Short zone identifier appended to var.project to form the managed zone resource name (e.g. \"primary\" -> \"<project>-primary\"). Only used when create_zone = true. Must satisfy Cloud DNS's zone-name format: lowercase letters, digits, hyphens; 1-63 chars; start with a letter."
  type        = string
  default     = "primary"

  validation {
    condition     = can(regex("^[a-z][-a-z0-9]{0,62}$", var.zone_short_name))
    error_message = "zone_short_name must start with a lowercase letter and contain only lowercase letters, digits, and hyphens (1-63 chars)."
  }
}

variable "zone_name" {
  description = "Existing Cloud DNS managed zone name (Cloud DNS resource name, e.g. \"example-com\"). Required when create_zone = false; ignored when create_zone = true."
  type        = string
  default     = null

  # Null-safe per Terraform's lack of short-circuit on ||.
  validation {
    condition     = var.zone_name == null ? true : can(regex("^[a-z][-a-z0-9]{0,62}$", var.zone_name))
    error_message = "zone_name must look like a Cloud DNS managed zone name: lowercase letters/digits/hyphens, start with a letter, 1-63 chars."
  }
}

variable "private_zone" {
  description = "If true, the (created or queried) managed zone is private. Private zones must be associated with at least one VPC via var.network_self_links."
  type        = bool
  default     = false
}

variable "network_self_links" {
  description = "VPC self-links to associate with a private managed zone (e.g. \"projects/<id>/global/networks/<name>\"). Required when private_zone = true and create_zone = true; ignored otherwise."
  type        = list(string)
  default     = []
}

variable "force_destroy" {
  description = "Allow `terraform destroy` to delete a created managed zone even when it still contains record sets. Has no effect when create_zone = false."
  type        = bool
  default     = false
}

variable "records" {
  description = <<-EOT
    Record sets — A / AAAA / CNAME / TXT / MX / SRV / NS / PTR / SOA / CAA.
    Each entry creates one `google_dns_record_set` with an explicit TTL. Use
    an empty `name` ("") to target the apex; non-empty names are interpreted
    as a left-hand label that this module concatenates with the zone's
    dns_name (e.g. name = "www" + dns_name = "example.com." -> "www.example.com.").

    Example:
      records = [
        { name = "www",  type = "CNAME", ttl = 300, values = ["target.example.com."] },
        { name = "",     type = "TXT",   ttl = 300, values = ["\"v=spf1 -all\""] },
        { name = "mail", type = "MX",    ttl = 300, values = ["10 inbound.example.com."] },
        { name = "api",  type = "A",     ttl = 60,  values = ["203.0.113.10"] },
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
      contains(["A", "AAAA", "CAA", "CNAME", "MX", "NS", "PTR", "SOA", "SRV", "TXT"], r.type)
    ])
    error_message = "Each record.type must be one of A, AAAA, CAA, CNAME, MX, NS, PTR, SOA, SRV, TXT. (Cloud DNS also supports DNSKEY/DS/IPSECKEY/NAPTR/SPF/SSHFP/TLSA but those are out of v1 scope — open a follow-up if needed.)"
  }

  validation {
    condition     = alltrue([for r in var.records : r.ttl >= 0 && r.ttl <= 2147483647])
    error_message = "Each record.ttl must be a non-negative 32-bit integer."
  }

  validation {
    condition     = alltrue([for r in var.records : length(r.values) > 0])
    error_message = "Each record must have at least one entry in `values`."
  }

  # Distinct (name, type) pairs — Cloud DNS rejects duplicate rrsets per
  # zone, and the locals' map-keying would silently collapse collisions.
  validation {
    condition     = length(distinct([for r in var.records : "${r.name}|${r.type}"])) == length(var.records)
    error_message = "Each (name, type) pair must be unique across var.records. Cloud DNS rejects duplicate record sets per zone."
  }

  # TXT / SPF rrdata values must be wrapped in literal double quotes per
  # Cloud DNS's rrdata format. Catching unquoted values at plan time
  # avoids a confusing API rejection at apply.
  validation {
    condition = alltrue([
      for r in var.records :
      contains(["TXT", "SPF"], r.type) ? alltrue([for v in r.values : can(regex("^\".*\"$", v))]) : true
    ])
    error_message = "TXT and SPF record values must be wrapped in literal double quotes (e.g. \"\\\"v=spf1 -all\\\"\")."
  }
}

variable "labels" {
  description = "Labels applied to the managed zone (the only labelable resource in this module). Merged with the canonical { project = var.project } baseline."
  type        = map(string)
  default     = {}
}

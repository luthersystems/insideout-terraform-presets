# -----------------------------------------------------------------------------
# GCP Cloud DNS — managed zone + record sets
# -----------------------------------------------------------------------------
# Owns a Cloud DNS public or private managed zone (either created here or
# looked up by name) plus record sets (A / AAAA / CNAME / TXT / MX / SRV /
# NS / PTR / SOA / CAA).
#
# Scoping (issue #583 — GCP parallel of #140's aws/route53 v1):
#   - DNSSEC, cross-project peering, forwarding zones, reverse-lookup
#     zones, and Cloud-DNS routing policies (geo / WRR / failover) are out
#     of scope for v1.
#   - Google-managed certificate issuance (Certificate Manager DNS
#     authorization, the ACM-equivalent) belongs in a future preset or a
#     follow-up iteration of this module.
#   - There is no native "alias" record in Cloud DNS — callers point at
#     load-balancer IPs / Cloud Run service IPs / etc. with plain A / AAAA
#     or CNAME entries via var.records.
#
# TODO(#583 follow-up): register google_dns_managed_zone and
# google_dns_record_set in pkg/insideout-import/registry/registry.go
# (gcpDiscoverTypes) and add a Cloud DNS inspector under
# pkg/observability/discovery/gcp/. Until then, this module's resources
# are not represented in the typed registry / drift inspector, so the
# `labels = merge({ project = var.project }, var.labels)` block on the
# managed zone below is NOT CI-enforced by tests/lint-project-label.sh
# (the allowlist there gates on registry parity per
# TestUntaggableAllowlistsMatchLintScripts). The label still propagates
# the Project identity correctly at apply time, matching the convention
# every other GCP preset follows.

terraform {
  required_version = ">= 1.0"
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = ">= 5.0"
    }
  }
}

# -----------------------------------------------------------------------------
# Enable Cloud DNS API
# -----------------------------------------------------------------------------
# Cloud DNS depends on the dns.googleapis.com service being enabled in the
# project. Unconditional so the test gate
# (TestEveryPresetHasUnconditionalResource) sees at least one default-input
# resource, and so the API enable lands before the managed zone CREATE.
resource "google_project_service" "dns" {
  project = var.project_id
  service = "dns.googleapis.com"

  disable_on_destroy = false
}

# -----------------------------------------------------------------------------
# Managed zone — either create or look up
# -----------------------------------------------------------------------------
# Exactly one of these resolves at any time:
#   - create_zone=true  -> google_dns_managed_zone.this is created
#   - create_zone=false -> data.google_dns_managed_zone.existing is queried
#
# Downstream resources reference local.zone_name, which selects the right
# source based on create_zone.

resource "google_dns_managed_zone" "this" {
  count = var.create_zone ? 1 : 0

  project     = var.project_id
  name        = "${var.project}-${var.zone_short_name}"
  dns_name    = var.dns_name
  description = "Managed zone for ${var.dns_name} (${var.project})"
  visibility  = var.private_zone ? "private" : "public"

  force_destroy = var.force_destroy

  depends_on = [google_project_service.dns]

  # Private zones require at least one VPC self-link. Public zones must
  # NOT carry a private_visibility_config block, so the dynamic block
  # collapses to zero when private_zone = false.
  dynamic "private_visibility_config" {
    for_each = var.private_zone ? [1] : []
    content {
      dynamic "networks" {
        for_each = toset(var.network_self_links)
        content {
          network_url = networks.value
        }
      }
    }
  }

  labels = merge({ project = var.project }, var.labels)
}

data "google_dns_managed_zone" "existing" {
  count = var.create_zone ? 0 : 1

  project = var.project_id
  name    = var.zone_name
}

locals {
  zone_name = var.create_zone ? google_dns_managed_zone.this[0].name : data.google_dns_managed_zone.existing[0].name
  zone_id   = var.create_zone ? google_dns_managed_zone.this[0].id : data.google_dns_managed_zone.existing[0].id
  dns_name  = var.create_zone ? google_dns_managed_zone.this[0].dns_name : data.google_dns_managed_zone.existing[0].dns_name

  # Build a stable map for record sets keyed by "<name>-<type>". Cloud DNS
  # rejects multiple record sets with the same (name, type) per zone, so
  # the key collisions surface as deterministic plan errors. Empty `name`
  # ("") targets the apex (resolved below to the zone's dns_name).
  records_map = {
    for r in var.records :
    "${r.name}-${r.type}" => r
  }
}

# -----------------------------------------------------------------------------
# Record sets (A / AAAA / CNAME / TXT / MX / SRV / NS / etc.)
# -----------------------------------------------------------------------------
# Cloud DNS requires fully-qualified record names ending with the zone's
# dns_name (which itself ends with a trailing dot). For convenience, an
# empty `name` ("") on the input targets the apex, and non-empty values
# are interpreted as a left-hand label (e.g. "www") that we concatenate
# with the zone's dns_name.
resource "google_dns_record_set" "records" {
  for_each = local.records_map

  project = var.project_id
  # google_dns_record_set has no `labels` attribute (Cloud DNS record
  # sets are not labelable). It also has no `tags`. The zone carries the
  # project label; record sets are scoped through the parent zone.

  managed_zone = local.zone_name
  name         = each.value.name == "" ? local.dns_name : "${each.value.name}.${local.dns_name}"
  type         = each.value.type
  ttl          = each.value.ttl
  rrdatas      = each.value.values
}

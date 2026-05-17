terraform {
  required_version = ">= 1.5"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 6.0"
    }
  }
}

# -----------------------------------------------------------------------------
# Module overview
# -----------------------------------------------------------------------------
# Owns a Route 53 public or private hosted zone (either created here or
# looked up by ID) plus two flavours of records:
#
#   - `var.records`  — plain CNAME / A / AAAA / TXT / MX / SRV / NS / etc.
#                      records with explicit TTL and values.
#   - `var.aliases`  — A/AAAA alias records that point at an AWS service
#                      endpoint (ALB, CloudFront, API Gateway, etc.). Alias
#                      records have no TTL and require the target's hosted
#                      zone ID.
#
# Scoping (issue #140 v1):
#   - DNSSEC, cross-account delegation, vanity domain registration, and
#     full ACM-cert issuance flows are out of scope for v1. ACM DNS-validation
#     plumbing belongs in a future `aws/acm` preset (or a follow-up
#     iteration of this module).

module "name" {
  source         = "github.com/luthersystems/tf-modules.git//luthername?ref=v55.15.0"
  luther_project = var.project
  aws_region     = var.region
  luther_env     = var.environment
  org_name       = "luthersystems"
  component      = "insideout"
  subcomponent   = "dns"
  resource       = "dns"
}

# -----------------------------------------------------------------------------
# Hosted zone — either create or look up
# -----------------------------------------------------------------------------
# Exactly one of these resolves at any time:
#   - create_zone=true  -> aws_route53_zone.this is created
#   - create_zone=false -> data.aws_route53_zone.existing is queried
#
# Downstream resources reference local.zone_id, which selects the right
# source based on create_zone.

resource "aws_route53_zone" "this" {
  count = var.create_zone ? 1 : 0

  name          = var.domain_name
  comment       = "Hosted zone for ${var.domain_name} (${var.project})"
  force_destroy = var.force_destroy

  # Private zone toggle requires at least one VPC association.
  dynamic "vpc" {
    for_each = var.private_zone ? toset(var.vpc_ids) : toset([])
    content {
      vpc_id = vpc.value
    }
  }

  tags = merge(module.name.tags, var.tags)
}

data "aws_route53_zone" "existing" {
  count = var.create_zone ? 0 : 1

  zone_id      = var.zone_id
  private_zone = var.private_zone
}

locals {
  zone_id   = var.create_zone ? aws_route53_zone.this[0].zone_id : data.aws_route53_zone.existing[0].zone_id
  zone_name = var.create_zone ? aws_route53_zone.this[0].name : data.aws_route53_zone.existing[0].name

  # Build a stable map for plain records keyed by "<name>-<type>" so a
  # caller can safely add/remove entries mid-stream without churning every
  # subsequent record. Empty name ("") is the apex.
  records_map = {
    for r in var.records :
    "${r.name}-${r.type}" => r
  }

  # Aliases keyed by "<name>" — alias records are A or AAAA, and Route 53
  # allows only one record set per (name, type) per zone, so the name alone
  # is sufficient when the caller stays in alias-A territory. For dual-stack
  # callers, append "-${r.type}" if/when AAAA aliases are added.
  aliases_map = {
    for a in var.aliases :
    "${a.name}-${try(a.type, "A")}" => a
  }
}

# -----------------------------------------------------------------------------
# Plain records (CNAME / A / TXT / MX / SRV / AAAA / NS / etc.)
# -----------------------------------------------------------------------------
resource "aws_route53_record" "records" {
  for_each = local.records_map

  zone_id = local.zone_id
  # Empty name maps to the apex; otherwise it's a subdomain label that
  # Route 53 concatenates with the zone name.
  name    = each.value.name
  type    = each.value.type
  ttl     = each.value.ttl
  records = each.value.values

  # aws_route53_record does not accept a tags attribute (Route 53 record
  # sets are not taggable resources in AWS). Tagging happens at the zone.
}

# -----------------------------------------------------------------------------
# Alias records (ALB / CloudFront / API Gateway / etc.)
# -----------------------------------------------------------------------------
# Alias records cannot carry a TTL — the target's TTL governs caching.
# `evaluate_target_health` only meaningfully toggles for ALB / NLB targets;
# CloudFront and API Gateway require it to be false.
resource "aws_route53_record" "aliases" {
  for_each = local.aliases_map

  zone_id = local.zone_id
  name    = each.value.name
  type    = try(each.value.type, "A")

  alias {
    name                   = each.value.target_dns_name
    zone_id                = each.value.target_zone_id
    evaluate_target_health = try(each.value.evaluate_target_health, false)
  }

  # aws_route53_record is not taggable; see note above.
}

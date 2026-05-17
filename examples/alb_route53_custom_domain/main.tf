# -----------------------------------------------------------------------------
# Example: ALB + Route 53 custom domain (issue #585, follow-up to #140)
# -----------------------------------------------------------------------------
# Demonstrates the end-to-end wiring requested by the Route 53 v1 acceptance
# criteria:
#
#   VPC  ->  ALB (public)  ->  Route 53 alias record at the apex
#
# The Route 53 module owns the hosted zone (create_zone = true here so the
# example is self-contained) and an apex A-alias record pointing at the ALB
# via `module.alb.alb_dns_name` / `module.alb.alb_zone_id`. That is the same
# alias wiring the InsideOut composer emits automatically when both presets
# are selected together (PR #589, issue #584).
#
# Prerequisites for a real deploy (not enforced by `terraform validate`):
#   - Delegate `var.route53_domain_name` to the hosted zone's name servers
#     (output `module.route53.name_servers`) at your registrar.
#   - HTTPS is out of scope here — the ALB listens on HTTP only. Add an ACM
#     certificate ARN to `module.alb.certificate_arn` and an extra DNS-validation
#     record to enable HTTPS. ACM is tracked separately (see route53 module
#     header in aws/route53/main.tf).
# -----------------------------------------------------------------------------

module "vpc" {
  source      = "../../aws/vpc"
  project     = var.vpc_project
  environment = var.environment
  region      = var.vpc_region
}

module "alb" {
  source            = "../../aws/alb"
  vpc_id            = module.vpc.vpc_id
  public_subnet_ids = module.vpc.public_subnet_ids
  project           = var.alb_project
  environment       = var.environment
  region            = var.alb_region
}

module "route53" {
  source = "../../aws/route53"

  project     = var.route53_project
  environment = var.environment
  region      = var.route53_region

  domain_name = var.route53_domain_name
  create_zone = var.route53_create_zone

  # Apex A-alias pointing at the ALB. evaluate_target_health = true is the
  # recommended setting for ALB targets (health-checked failover); CloudFront
  # / API Gateway targets must keep it false.
  aliases = [
    {
      name                   = "" # apex
      target_dns_name        = module.alb.alb_dns_name
      target_zone_id         = module.alb.alb_zone_id
      evaluate_target_health = true
    },
  ]
}

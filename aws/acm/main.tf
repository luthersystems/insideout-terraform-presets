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
# Owns an AWS Certificate Manager (ACM) certificate with DNS validation.
#
# Scope (issue #586 v1):
#   - Single ACM certificate per module instance, `validation_method = "DNS"`.
#   - Emits the DNS validation records as a structured output so the caller
#     can wire them into `aws/route53.records` (or any external DNS source).
#   - Optionally waits for validation to complete (`aws_acm_certificate_validation`),
#     gated on `var.create_validation` and the FQDN list returned after the
#     records are created downstream.
#   - EMAIL validation and re-import flows are out of scope. Use the AWS
#     console / cert-manager workflow if you need either.
#
# Region note:
#   ACM certificates for CloudFront MUST live in us-east-1 (CloudFront is a
#   global service that pins to the N. Virginia regional control plane). For
#   regional consumers (ALB, API Gateway), the cert must live in the same
#   region as the consumer.
#
#   This module does NOT declare a `configuration_aliases` provider alias —
#   instead, callers pin `var.region` (and the composer pins the underlying
#   provider region) per instance. Compose two ACM module instances when a
#   stack needs both an us-east-1 CloudFront cert and a regional ALB cert.
#
# Composer wiring (tracked in #593):
#   - `validation_records` output -> `aws/route53.records` input
#   - `certificate_arn` output -> ALB / API Gateway / CloudFront cert input
#   - `validation_record_fqdns` (caller-supplied) -> `aws_acm_certificate_validation.this`
# Until #593 lands, callers wire this module manually (see
# examples/alb_route53_custom_domain for the pattern).

module "name" {
  source         = "github.com/luthersystems/tf-modules.git//luthername?ref=v55.15.0"
  luther_project = var.project
  aws_region     = var.region
  luther_env     = var.environment
  org_name       = "luthersystems"
  component      = "insideout"
  subcomponent   = "acm"
  resource       = "cert"
}

# -----------------------------------------------------------------------------
# Certificate
# -----------------------------------------------------------------------------
# `create_before_destroy = true` is mandatory for ACM certs that back live
# listeners: replacing a cert (e.g. on SAN change) must provision the new one
# before AWS detaches the old, otherwise ALB / CloudFront briefly serve no
# cert and clients get TLS errors. AWS' own documentation calls this out.
resource "aws_acm_certificate" "this" {
  domain_name               = var.domain_name
  subject_alternative_names = var.subject_alternative_names
  validation_method         = "DNS"

  # Optional: pin the cert's certificate transparency policy. Public certs
  # default to ENABLED; only set DISABLED if the org has a CT logging
  # exception (rare; mostly internal-only PKI use cases).
  options {
    certificate_transparency_logging_preference = var.certificate_transparency_logging
  }

  # Optional: pin the cert's key algorithm (defaults to RSA_2048). ECDSA
  # certs (EC_prime256v1 / EC_secp384r1) trade broader client support for
  # smaller handshake payloads. ALB and CloudFront both support ECDSA.
  key_algorithm = var.key_algorithm

  tags = merge(module.name.tags, var.tags)

  lifecycle {
    create_before_destroy = true
  }
}

# -----------------------------------------------------------------------------
# Optional validation wait
# -----------------------------------------------------------------------------
# `aws_acm_certificate_validation` is a synchronous wait on AWS' side —
# Terraform blocks until the cert moves to ISSUED status. This requires the
# DNS validation records (from the `validation_records` output below) to
# already exist in DNS, which is the caller's responsibility (typically by
# wiring this module's output into `aws/route53.records`).
#
# Set `create_validation = false` when:
#   - The caller is using an external DNS provider that Terraform doesn't
#     manage (cert will eventually be ISSUED out-of-band).
#   - The caller wants to apply this module before wiring records and accept
#     a "PENDING_VALIDATION" cert in state on the first apply.
#
# `validation_record_fqdns` lets the caller pass back the exact FQDNs the
# records were written under, which avoids a chicken-and-egg dependency
# cycle between this module and the route53 module.
resource "aws_acm_certificate_validation" "this" {
  count = var.create_validation ? 1 : 0

  certificate_arn         = aws_acm_certificate.this.arn
  validation_record_fqdns = var.validation_record_fqdns

  # aws_acm_certificate_validation is not a taggable resource (it represents
  # a wait operation, not an AWS-side object).

  timeouts {
    create = var.validation_timeout
  }
}

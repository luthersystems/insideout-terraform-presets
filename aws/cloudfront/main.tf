terraform {
  required_version = ">= 1.5"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 6.0"
    }
  }
}

module "name" {
  source         = "github.com/luthersystems/tf-modules.git//luthername?ref=v55.15.0"
  luther_project = var.project
  aws_region     = var.region
  luther_env     = var.environment
  org_name       = "luthersystems"
  component      = "insideout"
  subcomponent   = "cloudfront"
  resource       = "cloudfront"
}

# Region is only used if we optionally create an S3 bucket for the origin.

# -----------------------------------------------------------------------------
# Optional S3 origin (demo/convenience)
# -----------------------------------------------------------------------------
resource "aws_s3_bucket" "origin" {
  count  = var.origin_type == "s3" && var.create_bucket ? 1 : 0
  bucket = var.s3_bucket_name != null ? var.s3_bucket_name : "${var.project}-cdn-origin"
  tags   = merge(module.name.tags, { Name = "${module.name.prefix}-origin" }, var.tags)
}

resource "aws_s3_bucket_public_access_block" "origin" {
  count                   = length(aws_s3_bucket.origin) == 0 ? 0 : 1
  bucket                  = aws_s3_bucket.origin[0].id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

# Simpler/older but reliable approach for private S3 origins
resource "aws_cloudfront_origin_access_identity" "oai" {
  count   = var.origin_type == "s3" ? 1 : 0
  comment = "${var.project} OAI"
}

# Allow CloudFront OAI to read from the created S3 bucket (if we created it)
data "aws_iam_policy_document" "origin_bucket" {
  count = length(aws_s3_bucket.origin) == 0 ? 0 : 1
  statement {
    sid     = "AllowCloudFrontRead"
    effect  = "Allow"
    actions = ["s3:GetObject"]
    principals {
      type        = "AWS"
      identifiers = [aws_cloudfront_origin_access_identity.oai[0].iam_arn]
    }
    resources = ["${aws_s3_bucket.origin[0].arn}/*"]
  }
}

resource "aws_s3_bucket_policy" "origin" {
  count  = length(aws_s3_bucket.origin) == 0 ? 0 : 1
  bucket = aws_s3_bucket.origin[0].id
  policy = data.aws_iam_policy_document.origin_bucket[0].json
}

# -----------------------------------------------------------------------------
# CloudFront Distribution
# -----------------------------------------------------------------------------
locals {
  # If using S3 origin:
  # - when create_bucket=true, take the created bucket name
  # - when create_bucket=false, use the provided var.s3_bucket_name
  resolved_s3_bucket = var.origin_type == "s3" ? (var.create_bucket ? try(aws_s3_bucket.origin[0].bucket, null) : var.s3_bucket_name) : null

  # CloudFront expects the S3 REST endpoint, not a website endpoint
  s3_domain_name = local.resolved_s3_bucket != null ? "${local.resolved_s3_bucket}.s3.amazonaws.com" : null

  origin_id   = var.origin_type == "s3" ? (local.resolved_s3_bucket != null ? "s3-${local.resolved_s3_bucket}" : "s3-UNSET") : "custom-${var.custom_origin_domain}"
  default_ttl = var.default_ttl_seconds
}

resource "aws_cloudfront_distribution" "this" {
  enabled             = true
  is_ipv6_enabled     = true
  comment             = "${var.project} distribution"
  price_class         = var.price_class
  default_root_object = var.default_root_object

  # Attach WAFv2 WebACL to CloudFront here (expects WebACL ARN)
  web_acl_id = var.web_acl_id

  aliases = var.aliases

  dynamic "origin" {
    for_each = var.origin_type == "s3" ? [1] : []
    content {
      domain_name = local.s3_domain_name
      origin_id   = local.origin_id

      s3_origin_config {
        origin_access_identity = aws_cloudfront_origin_access_identity.oai[0].cloudfront_access_identity_path
      }
      origin_path = var.origin_path
    }
  }

  dynamic "origin" {
    for_each = var.origin_type == "http" ? [1] : []
    content {
      domain_name = var.custom_origin_domain
      origin_id   = local.origin_id
      origin_path = var.origin_path

      custom_origin_config {
        http_port              = 80
        https_port             = 443
        origin_protocol_policy = "https-only"
        origin_ssl_protocols   = ["TLSv1.2"]
      }
    }
  }

  default_cache_behavior {
    allowed_methods        = ["GET", "HEAD", "OPTIONS"]
    cached_methods         = ["GET", "HEAD"]
    target_origin_id       = local.origin_id
    viewer_protocol_policy = "redirect-to-https"

    compress = true

    forwarded_values {
      query_string = var.forward_query_string
      cookies {
        forward = var.forward_cookies ? "all" : "none"
      }
    }

    min_ttl     = 0
    default_ttl = local.default_ttl
    max_ttl     = max(local.default_ttl, 86400)
  }

  restrictions {
    geo_restriction {
      restriction_type = "none"
    }
  }

  dynamic "logging_config" {
    for_each = var.enable_logging ? [1] : []
    content {
      bucket          = var.logging_bucket_domain # e.g. "my-logs-bucket.s3.amazonaws.com"
      prefix          = var.logging_prefix
      include_cookies = false
    }
  }

  viewer_certificate {
    acm_certificate_arn            = var.acm_certificate_arn
    ssl_support_method             = var.acm_certificate_arn == null ? null : "sni-only"
    minimum_protocol_version       = var.acm_certificate_arn == null ? null : "TLSv1.2_2021"
    cloudfront_default_certificate = var.acm_certificate_arn == null
  }

  tags = merge(module.name.tags, var.tags)

  # Basic guardrails: ensure required inputs per origin type
  lifecycle {
    ignore_changes = [viewer_certificate[0].minimum_protocol_version]
  }

  dynamic "custom_error_response" {
    for_each = var.custom_error_responses
    content {
      error_caching_min_ttl = lookup(custom_error_response.value, "error_caching_min_ttl", null)
      error_code            = custom_error_response.value.error_code
      response_code         = lookup(custom_error_response.value, "response_code", null)
      response_page_path    = lookup(custom_error_response.value, "response_page_path", null)
    }
  }
}

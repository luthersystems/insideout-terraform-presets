# GCP Cloud CDN Configuration
# Cloud CDN is enabled on the load balancer backend services.
# This module provides CDN-specific configuration like cache policies.

terraform {
  required_version = ">= 1.5"
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = ">= 5.0"
    }
  }
}

# Cloud CDN cache policy is configured via the load balancer backend service.
# This local captures the CDN configuration for reference and outputs.
locals {
  cdn_config = {
    enabled           = true
    cache_mode        = var.cache_mode
    default_ttl       = var.default_ttl
    max_ttl           = var.max_ttl
    client_ttl        = var.client_ttl
    negative_caching  = var.negative_caching
    serve_while_stale = var.serve_while_stale
  }
}

# GCP HTTP(S) Load Balancer Module using terraform-google-lb-http
# https://github.com/terraform-google-modules/terraform-google-lb-http

locals {
  name_prefix = "${var.project}-${var.name}"
}

# Health checks for backends
resource "google_compute_health_check" "this" {
  for_each = { for b in var.backends : b.name => b }

  name    = "${local.name_prefix}-${each.key}-hc"
  project = var.project

  http_health_check {
    port         = each.value.port
    request_path = each.value.health_check_path
  }

  check_interval_sec  = 10
  timeout_sec         = 5
  healthy_threshold   = 2
  unhealthy_threshold = 3
}

# Backend services
resource "google_compute_backend_service" "this" {
  for_each = { for b in var.backends : b.name => b }

  name        = "${local.name_prefix}-${each.key}"
  project     = var.project
  protocol    = each.value.protocol
  port_name   = each.value.port_name
  timeout_sec = each.value.timeout_sec

  enable_cdn = each.value.enable_cdn || var.enable_cdn

  dynamic "cdn_policy" {
    for_each = each.value.enable_cdn || var.enable_cdn ? [1] : []
    content {
      cache_mode                   = var.cdn_cache_mode
      signed_url_cache_max_age_sec = 3600
    }
  }

  health_checks = [google_compute_health_check.this[each.key].id]

  dynamic "backend" {
    for_each = each.value.instance_group != null ? [each.value.instance_group] : []
    content {
      group           = backend.value
      balancing_mode  = "UTILIZATION"
      capacity_scaler = 1.0
    }
  }

  dynamic "backend" {
    for_each = each.value.network_endpoint_group != null ? [each.value.network_endpoint_group] : []
    content {
      group           = backend.value
      balancing_mode  = "RATE"
      max_rate        = 10000
      capacity_scaler = 1.0
    }
  }

  dynamic "iap" {
    for_each = var.enable_iap ? [1] : []
    content {
      oauth2_client_id     = var.iap_oauth2_client_id
      oauth2_client_secret = var.iap_oauth2_client_secret
    }
  }

  security_policy = var.security_policy

  log_config {
    enable      = true
    sample_rate = 1.0
  }
}

# URL map
resource "google_compute_url_map" "this" {
  name            = "${local.name_prefix}-urlmap"
  project         = var.project
  default_service = length(var.backends) > 0 ? google_compute_backend_service.this[var.default_backend != "" ? var.default_backend : var.backends[0].name].id : null

  dynamic "host_rule" {
    for_each = var.url_map_hosts
    content {
      hosts        = host_rule.value.hosts
      path_matcher = host_rule.value.path_matcher
    }
  }

  dynamic "path_matcher" {
    for_each = var.path_matchers
    content {
      name            = path_matcher.value.name
      default_service = google_compute_backend_service.this[path_matcher.value.default_service].id

      dynamic "path_rule" {
        for_each = path_matcher.value.path_rules
        content {
          paths   = path_rule.value.paths
          service = google_compute_backend_service.this[path_rule.value.service].id
        }
      }
    }
  }
}

# Managed SSL certificate
resource "google_compute_managed_ssl_certificate" "this" {
  count = length(var.managed_ssl_domains) > 0 ? 1 : 0

  name    = "${local.name_prefix}-cert"
  project = var.project

  managed {
    domains = var.managed_ssl_domains
  }
}

# HTTPS proxy
resource "google_compute_target_https_proxy" "this" {
  count = var.enable_ssl ? 1 : 0

  name    = "${local.name_prefix}-https-proxy"
  project = var.project
  url_map = google_compute_url_map.this.id

  ssl_certificates = length(var.ssl_certificates) > 0 ? var.ssl_certificates : (
    length(var.managed_ssl_domains) > 0 ? [google_compute_managed_ssl_certificate.this[0].id] : []
  )
}

# HTTP proxy (for redirect or direct access)
resource "google_compute_target_http_proxy" "this" {
  name    = "${local.name_prefix}-http-proxy"
  project = var.project
  url_map = google_compute_url_map.this.id
}

# Global static IP
resource "google_compute_global_address" "this" {
  name    = "${local.name_prefix}-ip"
  project = var.project
}

# HTTPS forwarding rule
resource "google_compute_global_forwarding_rule" "https" {
  count = var.enable_ssl ? 1 : 0

  name       = "${local.name_prefix}-https"
  project    = var.project
  target     = google_compute_target_https_proxy.this[0].id
  port_range = "443"
  ip_address = google_compute_global_address.this.address

  labels = var.labels
}

# HTTP forwarding rule
resource "google_compute_global_forwarding_rule" "http" {
  name       = "${local.name_prefix}-http"
  project    = var.project
  target     = google_compute_target_http_proxy.this.id
  port_range = "80"
  ip_address = google_compute_global_address.this.address

  labels = var.labels
}


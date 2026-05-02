# GCP HTTP(S) Load Balancer Module using terraform-google-lb-http
# https://github.com/terraform-google-modules/terraform-google-lb-http

# Per-deploy suffix so retries after state loss don't 409 on the load
# balancer's named compute resources (issue #159). The suffix flows through
# local.name_prefix to every backend / health check / URL map / proxy /
# certificate / forwarding rule.
resource "random_id" "suffix" {
  byte_length = 4
}

locals {
  name_prefix = "${var.project}-${var.name}-${random_id.suffix.hex}"

  # HTTPS proxy + forwarding rule are only created when SSL is requested
  # AND a cert source is supplied. Without this guard, default settings
  # (var.enable_ssl = true, no certs) produce a target_https_proxy with an
  # empty ssl_certificates list, which GCP rejects with HTTP 412 at apply
  # (issue #166 part 3). With the guard, default settings yield an HTTP-only
  # LB; supplying ssl_certificates or managed_ssl_domains opts into HTTPS.
  https_enabled = var.enable_ssl && (
    length(var.ssl_certificates) > 0 || length(var.managed_ssl_domains) > 0
  )
}

# Health checks for backends
resource "google_compute_health_check" "this" {
  for_each = { for b in var.backends : b.name => b }

  name    = "${local.name_prefix}-${each.key}-hc"
  project = var.project_id

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
  project     = var.project_id
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
      enabled              = true
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
  project         = var.project_id
  default_service = length(var.backends) > 0 ? google_compute_backend_service.this[var.default_backend != "" ? var.default_backend : var.backends[0].name].id : null

  # GCP requires one of default_service / default_url_redirect on every URL map.
  dynamic "default_url_redirect" {
    for_each = length(var.backends) == 0 ? [1] : []
    content {
      host_redirect          = "placeholder.invalid"
      strip_query            = false
      redirect_response_code = "FOUND"
    }
  }

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
  project = var.project_id

  managed {
    domains = var.managed_ssl_domains
  }
}

# HTTPS proxy
resource "google_compute_target_https_proxy" "this" {
  count = local.https_enabled ? 1 : 0

  name    = "${local.name_prefix}-https-proxy"
  project = var.project_id
  url_map = google_compute_url_map.this.id

  ssl_certificates = length(var.ssl_certificates) > 0 ? var.ssl_certificates : (
    length(var.managed_ssl_domains) > 0 ? [google_compute_managed_ssl_certificate.this[0].id] : []
  )
}

# HTTP proxy (for redirect or direct access)
resource "google_compute_target_http_proxy" "this" {
  name    = "${local.name_prefix}-http-proxy"
  project = var.project_id
  url_map = google_compute_url_map.this.id
}

# Global static IP
resource "google_compute_global_address" "this" {
  name    = "${local.name_prefix}-ip"
  project = var.project_id

  labels = merge(
    {
      project = var.project
    },
    var.labels
  )
}

# HTTPS forwarding rule
resource "google_compute_global_forwarding_rule" "https" {
  count = local.https_enabled ? 1 : 0

  name       = "${local.name_prefix}-https"
  project    = var.project_id
  target     = google_compute_target_https_proxy.this[0].id
  port_range = "443"
  ip_address = google_compute_global_address.this.address

  labels = merge({ project = var.project }, var.labels)
}

# HTTP forwarding rule
resource "google_compute_global_forwarding_rule" "http" {
  name       = "${local.name_prefix}-http"
  project    = var.project_id
  target     = google_compute_target_http_proxy.this.id
  port_range = "80"
  ip_address = google_compute_global_address.this.address

  labels = merge({ project = var.project }, var.labels)
}


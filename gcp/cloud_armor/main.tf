# GCP Cloud Armor Security Policy
# https://registry.terraform.io/providers/hashicorp/google/latest/docs/resources/compute_security_policy

terraform {
  required_version = ">= 1.5"
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = ">= 5.0"
    }
  }
}

locals {
  policy_name = "${var.project}-${var.name}"
}

resource "google_compute_security_policy" "policy" {
  name        = local.policy_name
  project     = var.project_id
  description = var.description

  # Default rule — priority 2147483647 is required by the API and
  # functions as the catch-all. Override action via var.default_action.
  rule {
    action      = var.default_action
    priority    = "2147483647"
    description = "Default ${var.default_action} rule"
    match {
      versioned_expr = "SRC_IPS_V1"
      config {
        src_ip_ranges = ["*"]
      }
    }
  }

  dynamic "rule" {
    for_each = { for r in var.rules : r.priority => r }
    content {
      action      = rule.value.action
      priority    = rule.value.priority
      description = rule.value.description
      match {
        versioned_expr = "SRC_IPS_V1"
        config {
          src_ip_ranges = rule.value.src_ip_ranges
        }
      }
    }
  }

  dynamic "rule" {
    for_each = { for r in var.preconfigured_waf_rules : r.priority => r }
    content {
      action      = rule.value.action
      priority    = rule.value.priority
      description = "WAF: ${rule.value.expression}"
      match {
        expr {
          expression = "evaluatePreconfiguredExpr('${rule.value.expression}')"
        }
      }
    }
  }

  dynamic "rule" {
    for_each = var.rate_limit == null ? [] : [var.rate_limit]
    content {
      action      = "rate_based_ban"
      priority    = rule.value.priority
      description = "Rate limit"
      match {
        versioned_expr = "SRC_IPS_V1"
        config {
          src_ip_ranges = ["*"]
        }
      }
      rate_limit_options {
        conform_action   = "allow"
        exceed_action    = rule.value.exceed_action
        enforce_on_key   = rule.value.enforce_on_key
        ban_duration_sec = rule.value.ban_duration_sec
        rate_limit_threshold {
          count        = rule.value.count
          interval_sec = rule.value.interval_sec
        }
      }
    }
  }

  dynamic "adaptive_protection_config" {
    for_each = var.adaptive_protection_enabled ? [1] : []
    content {
      layer_7_ddos_defense_config {
        enable = true
      }
    }
  }

  labels = merge(
    {
      project = var.project
    },
    var.labels
  )
}

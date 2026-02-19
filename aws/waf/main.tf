terraform {
  required_version = ">= 1.5"
  required_providers {
    aws = {
      source                = "hashicorp/aws"
      version               = ">= 6.0"
      configuration_aliases = [aws.us_east_1]
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
  subcomponent   = "waf"
  resource       = "waf"
}

# Default provider (used for REGIONAL scope)

# WAF for CLOUDFRONT scope must use us-east-1 endpoint

locals {
  default_rules = [
    {
      name            = "AWSManagedRulesCommonRuleSet"
      vendor          = "AWS"
      priority        = 10
      override_action = "none"
    },
    {
      name            = "AWSManagedRulesAmazonIpReputationList"
      vendor          = "AWS"
      priority        = 20
      override_action = "none"
    },
    {
      name            = "AWSManagedRulesKnownBadInputsRuleSet"
      vendor          = "AWS"
      priority        = 30
      override_action = "none"
    }
  ]

  effective_rules = length(var.managed_rule_groups) > 0 ? var.managed_rule_groups : local.default_rules
}

# ---------------------------
# Web ACL (CLOUDFRONT-scope)
# ---------------------------
resource "aws_wafv2_web_acl" "cf" {
  count    = var.scope == "CLOUDFRONT" ? 1 : 0
  provider = aws.us_east_1

  name  = module.name.name
  scope = "CLOUDFRONT"

  default_action {
    allow {}
  }

  visibility_config {
    cloudwatch_metrics_enabled = true
    metric_name                = module.name.name
    sampled_requests_enabled   = true
  }

  dynamic "rule" {
    for_each = { for r in local.effective_rules : "${r.vendor}-${r.name}" => r }
    content {
      name     = rule.value.name
      priority = rule.value.priority

      override_action {
        dynamic "none" {
          for_each = rule.value.override_action == "none" ? [1] : []
          content {}
        }
        dynamic "count" {
          for_each = rule.value.override_action == "count" ? [1] : []
          content {}
        }
      }

      statement {
        managed_rule_group_statement {
          name        = rule.value.name
          vendor_name = rule.value.vendor
        }
      }

      visibility_config {
        cloudwatch_metrics_enabled = true
        metric_name                = "${module.name.name}-${rule.value.vendor}-${rule.value.name}"
        sampled_requests_enabled   = true
      }
    }
  }

  tags = merge(module.name.tags, var.tags)
}

# No association resource for CLOUDFRONT. Attach via CloudFront's web_acl_id.

# ---------------------------
# Web ACL (REGIONAL-scope)
# ---------------------------
resource "aws_wafv2_web_acl" "regional" {
  count = var.scope == "REGIONAL" ? 1 : 0

  name  = module.name.name
  scope = "REGIONAL"

  default_action {
    allow {}
  }

  visibility_config {
    cloudwatch_metrics_enabled = true
    metric_name                = module.name.name
    sampled_requests_enabled   = true
  }

  dynamic "rule" {
    for_each = { for r in local.effective_rules : "${r.vendor}-${r.name}" => r }
    content {
      name     = rule.value.name
      priority = rule.value.priority

      override_action {
        dynamic "none" {
          for_each = rule.value.override_action == "none" ? [1] : []
          content {}
        }
        dynamic "count" {
          for_each = rule.value.override_action == "count" ? [1] : []
          content {}
        }
      }

      statement {
        managed_rule_group_statement {
          name        = rule.value.name
          vendor_name = rule.value.vendor
        }
      }

      visibility_config {
        cloudwatch_metrics_enabled = true
        metric_name                = "${module.name.name}-${rule.value.vendor}-${rule.value.name}"
        sampled_requests_enabled   = true
      }
    }
  }

  tags = merge(module.name.tags, var.tags)
}

resource "aws_wafv2_web_acl_association" "regional" {
  count        = var.scope == "REGIONAL" ? 1 : 0
  resource_arn = var.resource_arn # e.g., ALB ARN in same region
  web_acl_arn  = aws_wafv2_web_acl.regional[0].arn
}

locals {
  web_acl_arn = var.scope == "CLOUDFRONT" ? aws_wafv2_web_acl.cf[0].arn : aws_wafv2_web_acl.regional[0].arn
  web_acl_id  = var.scope == "CLOUDFRONT" ? aws_wafv2_web_acl.cf[0].id : aws_wafv2_web_acl.regional[0].id
}

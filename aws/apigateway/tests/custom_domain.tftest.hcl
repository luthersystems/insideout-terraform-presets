mock_provider "aws" {
  override_resource {
    target = aws_apigatewayv2_domain_name.api
    values = {
      domain_name_configuration = {
        certificate_arn    = "arn:aws:acm:us-east-1:123456789012:certificate/abc"
        endpoint_type      = "REGIONAL"
        security_policy    = "TLS_1_2"
        target_domain_name = "d-abc123.execute-api.us-east-1.amazonaws.com"
        hosted_zone_id     = "Z1UJRXOUMOOFQ8"
      }
    }
  }
}

# Issue #925: the composer points a Route 53 alias at these outputs, so they
# must carry the custom domain's regional target, and be null without one.

run "no_custom_domain_outputs_null" {
  command = apply

  variables {
    project     = "test"
    region      = "us-east-1"
    environment = "test"
  }

  assert {
    condition     = output.target_domain_name == null && output.hosted_zone_id == null
    error_message = "alias target outputs must be null when domain_name is null"
  }
}

run "custom_domain_outputs_alias_target" {
  command = apply

  variables {
    project         = "test"
    region          = "us-east-1"
    environment     = "test"
    domain_name     = "api.example.com"
    certificate_arn = "arn:aws:acm:us-east-1:123456789012:certificate/abc"
  }

  assert {
    condition     = output.target_domain_name == "d-abc123.execute-api.us-east-1.amazonaws.com"
    error_message = "target_domain_name must come from domain_name_configuration"
  }

  assert {
    condition     = output.hosted_zone_id == "Z1UJRXOUMOOFQ8"
    error_message = "hosted_zone_id must come from domain_name_configuration"
  }
}

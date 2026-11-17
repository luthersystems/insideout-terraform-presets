mock_provider "aws" {
  override_resource {
    target = aws_cognito_user_pool_domain.custom
    values = {
      cloudfront_distribution         = "d111111abcdef8.cloudfront.net"
      cloudfront_distribution_arn     = "arn:aws:cloudfront::123456789012:distribution/EDFDVBD6EXAMPLE"
      cloudfront_distribution_zone_id = "Z2FDTNDATAQYW2"
    }
  }
}

# Issue #925: an optional custom domain beside the prefix domain, with the
# CloudFront alias target exposed for Route 53.

run "default_has_prefix_domain_only" {
  command = plan

  variables {
    project     = "test"
    region      = "us-east-1"
    environment = "test"
  }

  assert {
    condition     = length(aws_cognito_user_pool_domain.this) == 1 && length(aws_cognito_user_pool_domain.custom) == 0
    error_message = "default must keep the prefix domain and add no custom domain"
  }
}

run "custom_domain_outputs_cloudfront_alias_target" {
  command = apply

  variables {
    project                       = "test"
    region                        = "us-west-2"
    environment                   = "test"
    custom_domain                 = "auth.example.com"
    custom_domain_certificate_arn = "arn:aws:acm:us-east-1:123456789012:certificate/abc"
  }

  assert {
    condition     = aws_cognito_user_pool_domain.custom[0].domain == "auth.example.com" && aws_cognito_user_pool_domain.custom[0].certificate_arn == "arn:aws:acm:us-east-1:123456789012:certificate/abc"
    error_message = "custom domain resource must carry the domain and certificate"
  }

  assert {
    condition     = length(aws_cognito_user_pool_domain.this) == 1
    error_message = "the prefix domain must stay when a custom domain is added"
  }

  assert {
    condition     = output.custom_domain_cloudfront_distribution == "d111111abcdef8.cloudfront.net" && output.custom_domain_cloudfront_zone_id == "Z2FDTNDATAQYW2"
    error_message = "outputs must expose the CloudFront alias target"
  }
}

run "custom_domain_without_certificate_fails_at_plan" {
  command = plan

  variables {
    project       = "test"
    region        = "us-east-1"
    environment   = "test"
    custom_domain = "auth.example.com"
  }

  expect_failures = [aws_cognito_user_pool_domain.custom]
}

run "custom_domain_certificate_outside_us_east_1_rejected" {
  command = plan

  variables {
    project                       = "test"
    region                        = "us-west-2"
    environment                   = "test"
    custom_domain                 = "auth.example.com"
    custom_domain_certificate_arn = "arn:aws:acm:us-west-2:123456789012:certificate/abc"
  }

  expect_failures = [var.custom_domain_certificate_arn]
}

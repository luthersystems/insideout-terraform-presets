mock_provider "aws" {}

# The composer puts the ALB and CloudFront aliases in var.aliases and the
# API Gateway and Cognito custom-domain aliases in var.dependent_aliases
# (#925). They must land in separate resources, so the ALB record does not
# wait for Cognito, whose custom domain needs that record to exist first.
# aws_route53_record.aliases keeps its address, so live stacks see no
# replacement.

variables {
  project     = "test"
  region      = "us-east-1"
  environment = "test"
  domain_name = "example.com"
  create_zone = true
}

run "aliases_and_dependent_aliases_are_separate_resources" {
  command = plan

  variables {
    aliases = [
      {
        name                   = ""
        target_dns_name        = "alb-123.us-east-1.elb.amazonaws.com"
        target_zone_id         = "Z35SXDOTRQ7X7K"
        evaluate_target_health = true
      },
    ]
    dependent_aliases = [
      {
        name            = "auth"
        target_dns_name = "d111111abcdef8.cloudfront.net"
        target_zone_id  = "Z2FDTNDATAQYW2"
      },
    ]
  }

  assert {
    condition     = keys(aws_route53_record.aliases) == ["-A"]
    error_message = "the ALB alias must stay in aws_route53_record.aliases"
  }

  assert {
    condition     = keys(aws_route53_record.dependent_aliases) == ["auth-A"]
    error_message = "the Cognito alias must be in aws_route53_record.dependent_aliases"
  }

  assert {
    condition     = aws_route53_record.dependent_aliases["auth-A"].alias[0].evaluate_target_health == false
    error_message = "evaluate_target_health must default to false"
  }
}

run "dependent_aliases_default_empty" {
  command = plan

  assert {
    condition     = length(aws_route53_record.dependent_aliases) == 0
    error_message = "dependent_aliases must default to no records"
  }
}

run "same_name_in_both_variables_fails_plan" {
  command = plan

  variables {
    aliases = [
      {
        name            = "auth"
        target_dns_name = "alb-123.us-east-1.elb.amazonaws.com"
        target_zone_id  = "Z35SXDOTRQ7X7K"
      },
    ]
    dependent_aliases = [
      {
        name            = "auth"
        target_dns_name = "d111111abcdef8.cloudfront.net"
        target_zone_id  = "Z2FDTNDATAQYW2"
      },
    ]
  }

  expect_failures = [aws_route53_record.dependent_aliases]
}

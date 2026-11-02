mock_provider "aws" {}
mock_provider "random" {}

# Issue #910: aws/alb shape tests for the internal option. The default
# must stay internet-facing in the public subnets, because the composer
# does not set var.internal and existing stacks use the ALB as their
# public entry point. A change to aws_lb.internal replaces the ALB, so
# the default is pinned here.

run "alb_defaults_internet_facing" {
  command = plan

  variables {
    project           = "test"
    region            = "us-east-1"
    environment       = "test"
    vpc_id            = "vpc-12345"
    public_subnet_ids = ["subnet-pub-a", "subnet-pub-b"]
  }

  assert {
    condition     = aws_lb.alb.internal == false
    error_message = "The ALB must stay internet-facing by default (#910): the composer does not set internal, and a change replaces the ALB on live stacks."
  }

  assert {
    condition     = tolist(aws_lb.alb.subnets) == tolist(["subnet-pub-a", "subnet-pub-b"])
    error_message = "With the defaults, the ALB must use public_subnet_ids."
  }

  assert {
    condition     = length(aws_lb_listener.http_forward) == 1 && length(aws_lb_listener.https) == 0
    error_message = "With no certificate_arn, the preset must keep the HTTP forward listener and no HTTPS listener."
  }
}

run "alb_internal_uses_private_subnets" {
  command = plan

  variables {
    project            = "test"
    region             = "us-east-1"
    environment        = "test"
    vpc_id             = "vpc-12345"
    public_subnet_ids  = ["subnet-pub-a", "subnet-pub-b"]
    internal           = true
    private_subnet_ids = ["subnet-priv-a", "subnet-priv-b"]
  }

  assert {
    condition     = aws_lb.alb.internal == true
    error_message = "internal = true must make the ALB internal (#910)."
  }

  assert {
    condition     = tolist(aws_lb.alb.subnets) == tolist(["subnet-priv-a", "subnet-priv-b"])
    error_message = "An internal ALB must use private_subnet_ids when they are given, so it gets no address in a public subnet."
  }
}

run "alb_internal_without_private_subnets_falls_back" {
  command = plan

  variables {
    project           = "test"
    region            = "us-east-1"
    environment       = "test"
    vpc_id            = "vpc-12345"
    public_subnet_ids = ["subnet-pub-a", "subnet-pub-b"]
    internal          = true
  }

  assert {
    condition     = aws_lb.alb.internal == true && tolist(aws_lb.alb.subnets) == tolist(["subnet-pub-a", "subnet-pub-b"])
    error_message = "An internal ALB with no private_subnet_ids must use public_subnet_ids, so the caller can pass any subnets through the one wired input."
  }
}

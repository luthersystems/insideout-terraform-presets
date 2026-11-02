mock_provider "aws" {}
# Issue #907: opt-in narrow egress for the rds security group. The default
# must stay 0.0.0.0/0: the composer does not set egress_cidrs, and a
# narrower rule changes the network behaviour of live stacks on their
# next apply. egress_cidrs (for example the VPC CIDR) is the opt-in.

variables {
  project     = "test"
  region      = "us-east-1"
  environment = "test"
  vpc_id      = "vpc-12345"
  subnet_ids  = ["subnet-aaa", "subnet-bbb"]
}

run "rds_default_egress_is_open" {
  command = plan

  assert {
    condition     = one(aws_security_group.rds.egress).cidr_blocks == tolist(["0.0.0.0/0"])
    error_message = "The default egress must stay 0.0.0.0/0 (#907), so live stacks keep their network behaviour."
  }

}

run "rds_egress_cidrs_narrow_egress" {
  command = plan

  variables {
    egress_cidrs = ["10.0.0.0/16"]
  }

  assert {
    condition     = one(aws_security_group.rds.egress).cidr_blocks == tolist(["10.0.0.0/16"])
    error_message = "egress_cidrs must replace 0.0.0.0/0 in the egress rule."
  }

}

run "rds_rejects_bad_egress_cidr" {
  command = plan

  variables {
    egress_cidrs = ["not-a-cidr"]
  }

  expect_failures = [var.egress_cidrs]
}

run "rds_rejects_empty_egress_cidrs" {
  command = plan

  variables {
    egress_cidrs = []
  }

  expect_failures = [var.egress_cidrs]
}

# The egress rule sets cidr_blocks, which takes IPv4 only; an IPv6 CIDR
# would fail at apply time instead of at plan time.
run "rds_rejects_ipv6_egress_cidr" {
  command = plan

  variables {
    egress_cidrs = ["2001:db8::/32"]
  }

  expect_failures = [var.egress_cidrs]
}

# A CIDR with host bits set (10.0.0.1/16) is almost always a typo, and AWS
# would store it as 10.0.0.0/16, so the plan would never settle.
run "rds_rejects_host_bits_egress_cidr" {
  command = plan

  variables {
    egress_cidrs = ["10.0.0.1/16"]
  }

  expect_failures = [var.egress_cidrs]
}

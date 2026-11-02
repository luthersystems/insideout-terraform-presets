mock_provider "aws" {}
# Issue #907: opt-in narrow egress for the msk security group. The default
# must stay 0.0.0.0/0: the composer does not set egress_cidrs, and a
# narrower rule changes the network behaviour of live stacks on their
# next apply. egress_cidrs (for example the VPC CIDR) is the opt-in.

variables {
  project     = "test"
  region      = "us-east-1"
  environment = "test"
  vpc_id      = "vpc-12345"
  subnet_ids  = ["subnet-aaa", "subnet-bbb", "subnet-ccc"]
}

run "msk_default_egress_is_open" {
  command = plan

  assert {
    condition     = one(aws_security_group.msk.egress).cidr_blocks == tolist(["0.0.0.0/0"])
    error_message = "The default egress must stay 0.0.0.0/0 (#907), so live stacks keep their network behaviour."
  }

  assert {
    condition     = one(aws_security_group.msk.egress).self != true
    error_message = "The default open egress already admits broker-to-broker traffic, so it must not add self (that would change the plan of live clusters)."
  }
}

run "msk_egress_cidrs_narrow_egress" {
  command = plan

  variables {
    egress_cidrs = ["10.0.0.0/16"]
  }

  assert {
    condition     = one(aws_security_group.msk.egress).cidr_blocks == tolist(["10.0.0.0/16"])
    error_message = "egress_cidrs must replace 0.0.0.0/0 in the egress rule."
  }

  assert {
    condition     = one(aws_security_group.msk.egress).self == true
    error_message = "Narrowed egress must admit self, or brokers lose broker-to-broker traffic that the open rule allowed before."
  }
}

run "msk_rejects_bad_egress_cidr" {
  command = plan

  variables {
    egress_cidrs = ["not-a-cidr"]
  }

  expect_failures = [var.egress_cidrs]
}

run "msk_rejects_empty_egress_cidrs" {
  command = plan

  variables {
    egress_cidrs = []
  }

  expect_failures = [var.egress_cidrs]
}

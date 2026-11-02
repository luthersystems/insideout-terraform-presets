mock_provider "aws" {
  # data.aws_ami needs an ARM AMI id shape and an architecture the
  # preset's user_data accepts; the mock's random strings would not do.
  mock_data "aws_ami" {
    defaults = {
      id           = "ami-12345678"
      architecture = "arm64"
    }
  }

  # The role's assume_role_policy comes from this data source; the
  # provider checks that it is a JSON object at plan time.
  mock_data "aws_iam_policy_document" {
    defaults = {
      json = "{\"Version\":\"2012-10-17\",\"Statement\":[]}"
    }
  }
}

# Issue #908: the bastion's SSH ingress. The default must stay open to
# admin_cidrs (0.0.0.0/0): the composer does not set it, and removing the
# rule on a live bastion locks out key-pair and EC2 Instance Connect
# users on the next apply. admin_cidrs = [] is the opt-in for an
# SSM-only bastion with no inbound rule at all.

run "bastion_default_keeps_ssh_ingress" {
  command = plan

  variables {
    project     = "test"
    region      = "us-east-1"
    environment = "test"
    vpc_id      = "vpc-12345"
    subnet_id   = "subnet-aaa"
  }

  assert {
    condition     = length(aws_security_group.bastion_sg.ingress) == 1
    error_message = "The default bastion must keep its one SSH ingress rule (#908): removing it locks out live key-pair users."
  }

  assert {
    condition     = one(aws_security_group.bastion_sg.ingress).from_port == 22 && one(aws_security_group.bastion_sg.ingress).cidr_blocks == tolist(["0.0.0.0/0"])
    error_message = "The default SSH rule must admit port 22 from admin_cidrs (0.0.0.0/0 by default)."
  }
}

run "bastion_ssm_only_has_no_ingress" {
  command = plan

  variables {
    project     = "test"
    region      = "us-east-1"
    environment = "test"
    vpc_id      = "vpc-12345"
    subnet_id   = "subnet-aaa"
    admin_cidrs = []
  }

  assert {
    condition     = length(aws_security_group.bastion_sg.ingress) == 0
    error_message = "admin_cidrs = [] must remove the SSH ingress rule, so an SSM-only bastion accepts no inbound traffic (#908)."
  }
}

run "bastion_tight_admin_cidrs" {
  command = plan

  variables {
    project     = "test"
    region      = "us-east-1"
    environment = "test"
    vpc_id      = "vpc-12345"
    subnet_id   = "subnet-aaa"
    admin_cidrs = ["203.0.113.0/24"]
  }

  assert {
    condition     = one(aws_security_group.bastion_sg.ingress).cidr_blocks == tolist(["203.0.113.0/24"])
    error_message = "admin_cidrs must pass through to the SSH ingress rule."
  }
}

terraform {
  required_version = ">= 1.5"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 6.0"
    }
  }
}


module "name" {
  source         = "github.com/luthersystems/tf-modules.git//luthername?ref=v55.13.4"
  luther_project = var.project
  aws_region     = var.region
  luther_env     = var.environment
  org_name       = "luthersystems"
  component      = "insideout"
  subcomponent   = "ec2"
  resource       = "ec2"
}

locals {
  ami_id = var.ami_id != null ? var.ami_id : (
    var.os_type == "ubuntu" ? data.aws_ami.ubuntu[0].id : data.aws_ami.al2023[0].id
  )

  # Resolve effective user_data: inline script takes priority, URL generates a fetch wrapper.
  effective_user_data = var.user_data != "" ? var.user_data : (
    var.user_data_url != "" ? "#!/bin/bash\nset -euo pipefail\ncurl -fsSL '${var.user_data_url}' -o /tmp/user-data-script.sh\nchmod +x /tmp/user-data-script.sh\n/tmp/user-data-script.sh" : ""
  )
}

# Pick Amazon Linux 2023 AMI by arch (only when ami_id is not provided and os_type is amazon-linux)
data "aws_ami" "al2023" {
  count       = var.ami_id == null && var.os_type == "amazon-linux" ? 1 : 0
  owners      = ["137112412989"] # Amazon
  most_recent = true

  filter {
    name   = "name"
    values = ["al2023-ami-*-${var.arch}"]
  }
}

# Pick Ubuntu 24.04 LTS AMI by arch (only when ami_id is not provided and os_type is ubuntu)
data "aws_ami" "ubuntu" {
  count       = var.ami_id == null && var.os_type == "ubuntu" ? 1 : 0
  owners      = ["099720109477"] # Canonical
  most_recent = true

  filter {
    name   = "name"
    values = ["ubuntu/images/hvm-ssd-gp3/ubuntu-noble-24.04-${var.arch == "arm64" ? "arm64" : "amd64"}-server-*"]
  }

  filter {
    name   = "virtualization-type"
    values = ["hvm"]
  }
}

# -------------------------------------------------------------
# Security group
# -------------------------------------------------------------
resource "aws_security_group" "this" {
  name        = "${module.name.name}-sg"
  description = "Security group for ${var.project} EC2 instance"
  vpc_id      = var.vpc_id

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = merge(module.name.tags, { Name = "${module.name.prefix}-sg" }, var.tags)
}

data "aws_ip_ranges" "ec2_instance_connect" {
  count    = var.enable_instance_connect ? 1 : 0
  regions  = [var.region]
  services = ["EC2_INSTANCE_CONNECT"]
}

resource "aws_security_group_rule" "instance_connect_ssh" {
  count             = var.enable_instance_connect ? 1 : 0
  type              = "ingress"
  from_port         = 22
  to_port           = 22
  protocol          = "tcp"
  cidr_blocks       = data.aws_ip_ranges.ec2_instance_connect[0].cidr_blocks
  security_group_id = aws_security_group.this.id
  description       = "SSH from EC2 Instance Connect service IPs"
}

resource "aws_security_group_rule" "custom_ingress" {
  for_each = toset([for p in var.custom_ingress_ports : tostring(p)])

  type              = "ingress"
  from_port         = tonumber(each.value)
  to_port           = tonumber(each.value)
  protocol          = "tcp"
  cidr_blocks       = var.ingress_cidr_blocks
  security_group_id = aws_security_group.this.id
  description       = "Custom ingress on port ${each.value}"
}

# -------------------------------------------------------------
# IAM role + instance profile (SSM access)
# -------------------------------------------------------------
data "aws_iam_policy_document" "ec2_assume_role" {
  statement {
    effect = "Allow"
    principals {
      type        = "Service"
      identifiers = ["ec2.amazonaws.com"]
    }
    actions = ["sts:AssumeRole"]
  }
}

resource "aws_iam_role" "this" {
  name               = "${module.name.name}-role"
  assume_role_policy = data.aws_iam_policy_document.ec2_assume_role.json
  tags               = merge(module.name.tags, var.tags)
}

resource "aws_iam_role_policy_attachment" "ssm_core" {
  role       = aws_iam_role.this.name
  policy_arn = "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"
}

resource "aws_iam_instance_profile" "this" {
  name = "${module.name.name}-profile"
  role = aws_iam_role.this.name
}

# -------------------------------------------------------------
# SSH key pair (created only when ssh_public_key is provided)
# -------------------------------------------------------------
resource "aws_key_pair" "this" {
  count      = var.ssh_public_key != "" ? 1 : 0
  key_name   = "${module.name.name}-key"
  public_key = var.ssh_public_key
  tags       = merge(module.name.tags, var.tags)
}

# -------------------------------------------------------------
# EC2 instance
# -------------------------------------------------------------
resource "aws_instance" "this" {
  ami                         = local.ami_id
  instance_type               = var.instance_type
  subnet_id                   = var.subnet_id
  associate_public_ip_address = var.associate_public_ip
  vpc_security_group_ids      = [aws_security_group.this.id]
  iam_instance_profile        = aws_iam_instance_profile.this.name
  key_name                    = var.ssh_public_key != "" ? aws_key_pair.this[0].key_name : var.key_name

  user_data = local.effective_user_data != "" ? local.effective_user_data : null

  lifecycle {
    precondition {
      condition     = !(var.user_data != "" && var.user_data_url != "")
      error_message = "user_data and user_data_url are mutually exclusive. Set one or the other, not both."
    }
  }

  root_block_device {
    volume_size           = var.root_volume_size
    volume_type           = "gp3"
    delete_on_termination = true
    encrypted             = true
  }

  metadata_options {
    http_tokens = "required"
  }

  tags = merge(module.name.tags, var.tags)
}

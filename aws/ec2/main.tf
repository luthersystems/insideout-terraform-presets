terraform {
  required_version = ">= 1.5"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 6.0"
    }
  }
}


locals {
  common_tags = { Project = var.project }
  ami_id      = var.ami_id != null ? var.ami_id : data.aws_ami.al2023[0].id
}

# Pick Amazon Linux 2023 AMI by arch (only when ami_id is not provided)
data "aws_ami" "al2023" {
  count       = var.ami_id == null ? 1 : 0
  owners      = ["137112412989"] # Amazon
  most_recent = true

  filter {
    name   = "name"
    values = ["al2023-ami-*-${var.arch}"]
  }
}

# -------------------------------------------------------------
# Security group
# -------------------------------------------------------------
resource "aws_security_group" "this" {
  name        = "${var.project}-ec2-sg"
  description = "Security group for ${var.project} EC2 instance"
  vpc_id      = var.vpc_id

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = merge({ Name = "${var.project}-ec2-sg" }, local.common_tags, var.tags)
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
  name               = "${var.project}-ec2-role"
  assume_role_policy = data.aws_iam_policy_document.ec2_assume_role.json
  tags               = merge(local.common_tags, var.tags)
}

resource "aws_iam_role_policy_attachment" "ssm_core" {
  role       = aws_iam_role.this.name
  policy_arn = "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"
}

resource "aws_iam_instance_profile" "this" {
  name = "${var.project}-ec2-profile"
  role = aws_iam_role.this.name
}

# -------------------------------------------------------------
# SSH key pair (created only when ssh_public_key is provided)
# -------------------------------------------------------------
resource "aws_key_pair" "this" {
  count      = var.ssh_public_key != "" ? 1 : 0
  key_name   = "${var.project}-ec2-key"
  public_key = var.ssh_public_key
  tags       = merge(local.common_tags, var.tags)
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

  user_data = var.user_data != "" ? base64encode(var.user_data) : null

  metadata_options {
    http_tokens = "required"
  }

  tags = merge({ Name = "${var.project}-ec2" }, local.common_tags, var.tags)
}

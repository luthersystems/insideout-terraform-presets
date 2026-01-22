terraform {
  required_version = ">= 1.5"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 6.0"
    }
  }
}


# Pick Amazon Linux 2023 AMI by arch
data "aws_ami" "al2023" {
  owners      = ["137112412989"] # Amazon
  most_recent = true

  filter {
    name   = "name"
    values = ["al2023-ami-*-${var.arch}"]
  }
}

# Security group for bastion
resource "aws_security_group" "bastion_sg" {
  name        = "${var.project}-bastion-sg"
  description = "Security group for bastion host"
  vpc_id      = var.vpc_id

  ingress {
    description = "SSH from admin networks"
    from_port   = 22
    to_port     = 22
    protocol    = "tcp"
    cidr_blocks = var.admin_cidrs
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = merge({ Name = "${var.project}-bastion-sg" }, var.tags)
}

# Role for SSM Session Manager
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

resource "aws_iam_role" "bastion_role" {
  name               = "${var.project}-bastion-role"
  assume_role_policy = data.aws_iam_policy_document.ec2_assume_role.json
  tags               = var.tags
}

resource "aws_iam_role_policy_attachment" "ssm_core" {
  role       = aws_iam_role.bastion_role.name
  policy_arn = "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"
}

resource "aws_iam_instance_profile" "bastion_profile" {
  name = "${var.project}-bastion-profile"
  role = aws_iam_role.bastion_role.name
}

# locals for template vars
locals {
  arch_str            = var.arch == "arm64" ? "arm64" : "amd64"
  install_eks_tools_s = var.install_eks_tools ? "true" : "false"
}

# Bastion EC2 instance (public subnet)
resource "aws_instance" "bastion" {
  ami                         = data.aws_ami.al2023.id
  instance_type               = var.instance_type
  subnet_id                   = var.subnet_id
  associate_public_ip_address = true
  vpc_security_group_ids      = [aws_security_group.bastion_sg.id]
  iam_instance_profile        = aws_iam_instance_profile.bastion_profile.name
  key_name                    = var.key_name

  user_data = templatefile("${path.module}/user_data.sh.tmpl", {
    arch              = local.arch_str
    install_eks_tools = local.install_eks_tools_s
  })

  metadata_options {
    http_tokens = "required"
  }

  tags = merge({ Name = "${var.project}-bastion" }, var.tags)
}

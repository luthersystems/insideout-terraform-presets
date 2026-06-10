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
  source         = "github.com/luthersystems/tf-modules.git//luthername?ref=v55.15.0"
  luther_project = var.project
  aws_region     = var.region
  luther_env     = var.environment
  org_name       = "luthersystems"
  component      = "insideout"
  subcomponent   = "ec2"
  resource       = "ec2"
}

locals {
  # NVIDIA-GPU x86_64 EC2 families (#759). MUST stay in lockstep with the
  # HCL `_gpu_x86_families` local in aws/eks_nodegroup/main.tf and the Go
  # `gpuX86Families` set in pkg/composer/gpu.go — TestGPUFamiliesDrift parses
  # all three and fails the moment any one drifts. The GPU AMI is x86_64-only,
  # so gpu_enabled with a non-x86-GPU instance type (ARM, or a non-GPU family)
  # is rejected at plan by the aws_instance precondition below. g5g is
  # deliberately ABSENT: it is Graviton (ARM) + NVIDIA T4G with no x86 NVIDIA
  # AMI, so it stays on the ARM standard path.
  _gpu_x86_families = [
    "g4dn", "g5", "g6", "g6e", "gr6",
    "p3", "p3dn", "p4d", "p4de", "p5", "p5e", "p5en",
  ]
  _instance_family   = split(".", var.instance_type)[0]
  _is_gpu_x86_family = contains(local._gpu_x86_families, local._instance_family)

  # GPU path (#759) wins over os_type: when gpu_enabled and no explicit
  # ami_id, select the AWS Deep Learning Base GPU AMI (AL2023, NVIDIA
  # driver + container toolkit baked in) so a g5/g6/p4d/p5 instance comes
  # up with /dev/nvidia* present. Non-GPU path keeps the existing
  # os_type/arch selection. The NVIDIA AMI is x86_64-only (enforced by the
  # aws_instance precondition), so this never collides with arm64.
  ami_id = var.ami_id != null ? var.ami_id : (
    var.gpu_enabled ? data.aws_ami.gpu[0].id : (
      var.os_type == "ubuntu" ? data.aws_ami.ubuntu[0].id : data.aws_ami.al2023[0].id
    )
  )

  # Resolve effective user_data: inline script takes priority, URL generates a fetch wrapper.
  effective_user_data = var.user_data != "" ? var.user_data : (
    var.user_data_url != "" ? "#!/bin/bash\nset -euo pipefail\ncurl -fsSL '${var.user_data_url}' -o /tmp/user-data-script.sh\nchmod +x /tmp/user-data-script.sh\n/tmp/user-data-script.sh" : ""
  )
}

# Pick the AWS Deep Learning Base GPU AMI (Amazon Linux 2023) when GPU is
# requested and no explicit ami_id is given (#759). This AMI is published by
# Amazon (owner alias `amazon`) and ships the NVIDIA kernel driver + container
# toolkit pre-installed — no app-layer driver bootstrap needed on the host.
# x86_64-only (var.gpu_enabled requires arch=x86_64). The "Base" variant
# carries drivers without a bundled DL framework, keeping the image lean.
data "aws_ami" "gpu" {
  count       = var.ami_id == null && var.gpu_enabled ? 1 : 0
  owners      = ["amazon"]
  most_recent = true

  filter {
    name   = "name"
    values = ["Deep Learning Base OSS Nvidia Driver GPU AMI (Amazon Linux 2023)*"]
  }

  filter {
    name   = "architecture"
    values = ["x86_64"]
  }

  filter {
    name   = "virtualization-type"
    values = ["hvm"]
  }
}

# Pick Amazon Linux 2023 AMI by arch (only when ami_id is not provided, GPU is
# off, and os_type is amazon-linux)
data "aws_ami" "al2023" {
  count       = var.ami_id == null && !var.gpu_enabled && var.os_type == "amazon-linux" ? 1 : 0
  owners      = ["137112412989"] # Amazon
  most_recent = true

  filter {
    name   = "name"
    values = ["al2023-ami-*-${var.arch}"]
  }
}

# Pick Ubuntu 24.04 LTS AMI by arch (only when ami_id is not provided, GPU is
# off, and os_type is ubuntu)
data "aws_ami" "ubuntu" {
  count       = var.ami_id == null && !var.gpu_enabled && var.os_type == "ubuntu" ? 1 : 0
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

  # ingress is managed entirely by aws_vpc_security_group_ingress_rule
  # siblings below. Mixing inline egress + sibling ingress causes the
  # provider to re-read both aggregates on refresh, which drift-check
  # then flags.
  lifecycle {
    ignore_changes = [ingress, egress]
  }
}

data "aws_ip_ranges" "ec2_instance_connect" {
  count    = var.enable_instance_connect ? 1 : 0
  regions  = [var.region]
  services = ["EC2_INSTANCE_CONNECT"]
}

# The legacy aws_security_group_rule resource carries a synthetic
# `sgrule-<hash>` import ID that CloudFormation can't model. The
# aws_vpc_security_group_ingress_rule replacement (TF provider v5+)
# carries the real EC2-API security_group_rule_id (`sgr-XXXXX`) and is
# CFN/CC-feasible — required for the InsideOut import/discovery pipeline
# to round-trip these resources. See issue #460.
#
# Each replacement rule takes a single cidr_ipv4 (not a list), so the
# previous one-rule-with-N-CIDRs semantics fan out into N rules via
# for_each. Aggregate security posture is identical.
resource "aws_vpc_security_group_ingress_rule" "instance_connect_ssh" {
  for_each = var.enable_instance_connect ? toset(data.aws_ip_ranges.ec2_instance_connect[0].cidr_blocks) : toset([])

  security_group_id = aws_security_group.this.id
  ip_protocol       = "tcp"
  from_port         = 22
  to_port           = 22
  cidr_ipv4         = each.value
  description       = "SSH from EC2 Instance Connect service IPs"
  tags              = merge(module.name.tags, var.tags)
}

resource "aws_vpc_security_group_ingress_rule" "custom_ingress" {
  for_each = {
    for tuple in flatten([
      for port in var.custom_ingress_ports : [
        for cidr in var.ingress_cidr_blocks : {
          key  = "${port}-${cidr}"
          port = port
          cidr = cidr
        }
      ]
    ]) : tuple.key => tuple
  }

  security_group_id = aws_security_group.this.id
  ip_protocol       = "tcp"
  from_port         = each.value.port
  to_port           = each.value.port
  cidr_ipv4         = each.value.cidr
  description       = "Custom ingress on port ${each.value.port}"
  tags              = merge(module.name.tags, var.tags)
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
  tags               = merge(module.name.tags, var.tags)
}

resource "aws_iam_role_policy_attachment" "ssm_core" {
  role       = aws_iam_role.this.name
  policy_arn = "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"
}

resource "aws_iam_instance_profile" "this" {
  name = "${var.project}-ec2-profile"
  role = aws_iam_role.this.name
  tags = merge(module.name.tags, var.tags)
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

  # 1-minute CloudWatch metrics (default 5-minute). Required for any reliable2
  # chart that samples CPU/net/disk at sub-5-minute granularity.
  monitoring = true

  user_data = local.effective_user_data != "" ? local.effective_user_data : null

  lifecycle {
    precondition {
      condition     = !(var.user_data != "" && var.user_data_url != "")
      error_message = "user_data and user_data_url are mutually exclusive. Set one or the other, not both."
    }

    # GPU AMI is x86_64-only (#759): when gpu_enabled and no explicit ami_id,
    # the module boots the AWS Deep Learning Base GPU AMI, which exists only
    # for x86_64 NVIDIA GPU families (g4dn/g5/g6/g6e/gr6/p3/p3dn/p4d/p4de/p5/
    # p5e/p5en). A non-GPU type (t3.medium) or an ARM/Graviton type (c7g, g5g)
    # would boot a node with the GPU AMI's driver but no NVIDIA hardware, so
    # /dev/nvidia* never appears — the EC2 analogue of the EKS #207 arch
    # mismatch. Reject at plan rather than silently provisioning a useless GPU
    # node. Direct module callers who supply their own ami_id are exempt (the
    # ami_id==null guard). The composer always defaults/validates an x86 GPU
    # family upstream, so this fires only for direct/hand-rolled module use.
    # Not enforceable as a var validation block — TF forbids cross-variable
    # conditions there — so it lives as a resource precondition.
    precondition {
      condition     = !(var.gpu_enabled && var.ami_id == null) || local._is_gpu_x86_family
      error_message = "gpu_enabled=true requires an x86 NVIDIA GPU instance_type (one of g4dn/g5/g6/g6e/gr6/p3/p3dn/p4d/p4de/p5/p5e/p5en) when ami_id is null — AWS GPU AMIs are x86_64-only and a non-GPU/ARM type boots without /dev/nvidia*. Pick a supported GPU instance_type, or supply an explicit ami_id."
    }

    # GPU AMI is os_type-independent (#759): when gpu_enabled and no explicit
    # ami_id, the module always boots the AWS Deep Learning Base GPU AMI
    # (Amazon Linux 2023) regardless of os_type. Previously os_type="ubuntu"
    # was silently ignored on the GPU path — a caller asking for Ubuntu got an
    # Amazon Linux node with no warning. Reject the combination loudly so the
    # caller either drops os_type (accept the AL2023 GPU AMI) or supplies their
    # own Ubuntu GPU ami_id. Not enforceable as a var validation block — TF
    # forbids cross-variable conditions there — so it lives as a resource
    # precondition.
    precondition {
      condition     = !(var.gpu_enabled && var.ami_id == null && var.os_type == "ubuntu")
      error_message = "gpu_enabled=true selects the Amazon Linux 2023 GPU AMI and ignores os_type=\"ubuntu\". Drop os_type (or set it to \"amazon-linux\") to use the built-in GPU AMI, or supply an explicit Ubuntu GPU ami_id."
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

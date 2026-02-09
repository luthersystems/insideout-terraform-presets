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
}

# -------------------------------------------------------------
# IAM role for the managed node group (created only if needed)
# -------------------------------------------------------------
data "aws_iam_policy_document" "mng_assume" {
  statement {
    effect = "Allow"
    principals {
      type        = "Service"
      identifiers = ["ec2.amazonaws.com"]
    }
    actions = ["sts:AssumeRole"]
  }
}

# Create a role only when node_role_arn is NOT provided
resource "aws_iam_role" "mng" {
  count              = var.node_role_arn == null ? 1 : 0
  name               = "${var.cluster_name}-node-role"
  assume_role_policy = data.aws_iam_policy_document.mng_assume.json
  tags               = local.common_tags
}

# Standard policies for EKS worker nodes (only when we created the role)
resource "aws_iam_role_policy_attachment" "mng_worker" {
  count      = var.node_role_arn == null ? 1 : 0
  role       = aws_iam_role.mng[0].name
  policy_arn = "arn:aws:iam::aws:policy/AmazonEKSWorkerNodePolicy"
}

resource "aws_iam_role_policy_attachment" "mng_cni" {
  count      = var.node_role_arn == null ? 1 : 0
  role       = aws_iam_role.mng[0].name
  policy_arn = "arn:aws:iam::aws:policy/AmazonEKS_CNI_Policy"
}

resource "aws_iam_role_policy_attachment" "mng_ecr" {
  count      = var.node_role_arn == null ? 1 : 0
  role       = aws_iam_role.mng[0].name
  policy_arn = "arn:aws:iam::aws:policy/AmazonEC2ContainerRegistryReadOnly"
}

# Optional but handy for SSM Session Manager access to nodes
resource "aws_iam_role_policy_attachment" "mng_ssm" {
  count      = var.node_role_arn == null ? 1 : 0
  role       = aws_iam_role.mng[0].name
  policy_arn = "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"
}

# -------------------------------------------------------------
# EKS Managed Node Group
# -------------------------------------------------------------
resource "aws_eks_node_group" "this" {
  cluster_name    = var.cluster_name
  node_group_name = coalesce(var.node_group_name, "default")

  # Use caller-supplied role if provided; otherwise the role we created.
  # try(...) avoids errors when count = 0 on aws_iam_role.mng.
  node_role_arn = coalesce(
    var.node_role_arn,
    try(aws_iam_role.mng[0].arn, null)
  )

  subnet_ids = var.subnet_ids

  scaling_config {
    desired_size = var.desired_size
    min_size     = var.min_size
    max_size     = var.max_size
  }

  # Required; use c7i/c7g/etc. passed from root (computed by composer/mapper)
  instance_types = var.instance_types

  # When null the provider defaults to ON_DEMAND
  capacity_type = var.capacity_type

  # Merge caller-provided labels on top of our default
  labels = merge(
    { role = "app" },
    var.labels
  )

  update_config {
    max_unavailable_percentage = 33
  }

  # Merge module/common tags + caller-provided tags
  tags = merge(
    local.common_tags,
    var.tags,
    {
      Name   = coalesce(var.node_group_name, "default")
      backup = "true"
    }
  )
}

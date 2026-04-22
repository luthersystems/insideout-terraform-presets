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
  subcomponent   = "eks-nodegroup"
  resource       = "eks-nodegroup"
}

locals {
  common_tags = module.name.tags
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
  tags               = merge(local.common_tags, var.tags)
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
# Service-linked role bootstrap
# -------------------------------------------------------------
# EKS managed node groups need AWSServiceRoleForAmazonEKSNodegroup in the
# account. AWS's CreateNodegroup API is documented to auto-create it, but we
# bootstrap defensively to avoid IAM-propagation races on brand-new accounts
# and to survive cases where the nodegroup is created before the cluster has
# finished materialising its own SLR. Same pattern as aws/opensearch: probe
# with a plural data source (no error on zero matches) and only create when
# absent.
data "aws_iam_roles" "eks_nodegroup_slr" {
  name_regex  = "^AWSServiceRoleForAmazonEKSNodegroup$"
  path_prefix = "/aws-service-role/eks-nodegroup.amazonaws.com/"
}

resource "aws_iam_service_linked_role" "eks_nodegroup" {
  count            = length(data.aws_iam_roles.eks_nodegroup_slr.names) == 0 ? 1 : 0
  aws_service_name = "eks-nodegroup.amazonaws.com"
  description      = "Service-linked role for EKS managed node groups"
}

# -------------------------------------------------------------
# EKS Managed Node Group
# -------------------------------------------------------------
resource "aws_eks_node_group" "this" {
  depends_on = [aws_iam_service_linked_role.eks_nodegroup]

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

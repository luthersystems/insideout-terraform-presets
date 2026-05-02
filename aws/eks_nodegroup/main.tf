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

  # Auto-derive ami_type from the first instance type's family so ARM/Graviton
  # node groups (c7g.large, m7g.xlarge, etc.) don't silently fall back to the
  # provider's x86 default and produce DEGRADED addons (#207). Graviton/ARM
  # EC2 families end in `g` (e.g. c7g, m7g, r7g, t4g, c8g, m8g, r8g; with the
  # optional `d`, `n`, `en`, `ad`, `adn` storage/network suffixes).
  # Match the family prefix before the size suffix: `c7g.large` → family
  # `c7g` → ARM. The first instance type drives the choice; mixed-arch
  # instance lists are not supported by EKS managed node groups (homogeneous
  # AMI requirement) and are rejected upstream regardless. Caller can
  # override via var.ami_type.
  _first_instance_type = length(var.instance_types) > 0 ? var.instance_types[0] : "c7i.large"
  _instance_family     = split(".", local._first_instance_type)[0]
  _is_arm_family       = can(regex("^[a-z]+[0-9]+g(d|n|en|ad|adn)?$", local._instance_family))

  derived_ami_type  = local._is_arm_family ? "AL2023_ARM_64_STANDARD" : "AL2023_x86_64_STANDARD"
  resolved_ami_type = coalesce(var.ami_type, local.derived_ami_type)
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

# CloudWatchAgentServerPolicy grants cloudwatch:PutMetricData + the
# logs:CreateLogStream/PutLogEvents needed by the
# amazon-cloudwatch-observability addon's CloudWatch agent + fluent-bit
# DaemonSets. Attached only when this module creates the role; callers
# that supply node_role_arn are responsible for the equivalent grant.
resource "aws_iam_role_policy_attachment" "mng_cloudwatch_agent" {
  count      = var.node_role_arn == null && var.enable_container_insights ? 1 : 0
  role       = aws_iam_role.mng[0].name
  policy_arn = "arn:aws:iam::aws:policy/CloudWatchAgentServerPolicy"
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

  # Auto-derived from the first instance type's family unless var.ami_type is
  # set explicitly. Without this argument the provider defaults to
  # AL2023_x86_64_STANDARD, which silently breaks ARM instance choices like
  # c7g.large (#207).
  ami_type = local.resolved_ami_type

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

# -------------------------------------------------------------
# CloudWatch Container Insights addon
# -------------------------------------------------------------
# amazon-cloudwatch-observability installs the CloudWatch agent +
# fluent-bit DaemonSets in-cluster so node + pod metrics publish under
# the ContainerInsights namespace (node_cpu_utilization,
# pod_memory_utilization, etc.). Without the addon, AWS/EKS itself only
# publishes a small set of cluster-level metrics — see issue #233 / #231.
#
# Default-on (cliff per #233 Option B-1): existing deployments that
# haven't opted out will install the addon on the next apply and the
# ContainerInsights panel begins populating ~5 minutes later.
# CloudWatch ingest cost (~$0.30/GB) is the trade-off; opt out via
# var.enable_container_insights = false.
#
# Depends on the node group being up so the addon's DaemonSets can
# schedule, and on the CloudWatchAgentServerPolicy attachment so the
# agent has cloudwatch:PutMetricData when it starts.
resource "aws_eks_addon" "cloudwatch_observability" {
  count = var.enable_container_insights ? 1 : 0

  cluster_name = var.cluster_name
  addon_name   = "amazon-cloudwatch-observability"

  # OVERWRITE on create lets us adopt an existing addon if one was
  # installed out-of-band. PRESERVE on update means hand-tuned in-
  # cluster customizations (e.g. agent ConfigMap edits) survive subsequent
  # applies — at the cost of silently drifting from the addon's
  # default config. Customers who want a forced re-sync can re-create
  # the addon.
  resolve_conflicts_on_create = "OVERWRITE"
  resolve_conflicts_on_update = "PRESERVE"

  tags = merge(local.common_tags, var.tags)

  depends_on = [
    aws_eks_node_group.this,
    aws_iam_role_policy_attachment.mng_cloudwatch_agent,
  ]
}

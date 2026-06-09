# EKS managed node group preset.
#
# GPU note (#759): selecting an NVIDIA GPU instance family (g4dn/g5/g6/g6e/
# gr6/p3/p4d/p5, ...) auto-derives a GPU AMI type (AL2023_x86_64_NVIDIA), so
# the worker boots with the NVIDIA kernel driver present. This preset only
# provisions GPU-CAPABLE nodes. The in-cluster NVIDIA k8s device plugin that
# advertises `nvidia.com/gpu` to the scheduler is APP-LAYER and intentionally
# out of preset scope — EKS has no first-party managed addon for it and Helm
# is excluded from this repo, so it is installed by the deploying application.
# g/p families are quota-gated; surface capacity errors to the operator.

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

  # NVIDIA-GPU EC2 families (#759): g4dn, g5, g5g, g6, g6e, gr6, p3, p3dn,
  # p4d, p4de, p5, p5e, p5en. These need a GPU-bundled AMI type so the
  # NVIDIA kernel driver + container runtime are present on the node — a
  # plain AL2023_x86_64_STANDARD AMI on a g5.xlarge brings the worker up
  # but exposes no /dev/nvidia* devices, so GPU pods sit Pending forever
  # (the GPU analogue of the #207 arch mismatch). We match the family with
  # an explicit allow-list rather than a loose regex so that non-GPU
  # families that merely start with `g`/`p` (none today, but e.g. a future
  # general-purpose `g`-prefixed family) don't get misclassified.
  #
  # g5g is Graviton (ARM) + NVIDIA T4G — it ends in `g`, so the ARM regex
  # above already claims it; we deliberately leave g5g on the ARM path
  # (AL2023_ARM_64_STANDARD) because EKS has no ARM NVIDIA managed AMI type.
  # All other GPU families here are x86_64 → AL2023_x86_64_NVIDIA.
  _gpu_x86_families = [
    "g4dn", "g5", "g6", "g6e", "gr6",
    "p3", "p3dn", "p4d", "p4de", "p5", "p5e", "p5en",
  ]
  _is_gpu_x86_family = contains(local._gpu_x86_families, local._instance_family)

  # GPU x86 wins over the generic x86 default; ARM (incl. g5g) keeps the ARM
  # standard AMI. The NVIDIA in-cluster device plugin that advertises
  # nvidia.com/gpu to the scheduler is app-layer and intentionally out of
  # preset scope (no first-party EKS managed addon; see README + #759).
  derived_ami_type = (
    local._is_arm_family ? "AL2023_ARM_64_STANDARD" : (
      local._is_gpu_x86_family ? "AL2023_x86_64_NVIDIA" : "AL2023_x86_64_STANDARD"
    )
  )
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
# Tag propagation to EC2 instances (CLAUDE.md / issue #81)
# -------------------------------------------------------------
# `aws_eks_node_group.this.tags` tag the node-group RESOURCE only —
# AWS does NOT propagate them to the EC2 instances spawned by the
# managed node group's auto-derived ASG. This was discovered live on
# cust2 (project `io-hrbs5zprbk51`): five running EKS workers carried
# the AWS-managed `eks:cluster-name` tag but had no `Project` tag, so
# reliable3's `Project`-scoped EC2 inspector returned zero.
#
# `aws_autoscaling_group_tag` writes each tag onto the underlying ASG
# with `propagate_at_launch = true` — newly launched instances inherit
# every tag in `local.common_tags + var.tags` (Project, Environment,
# Component, customer-supplied additions). Already-running instances
# do NOT pick up the tag retroactively; a node refresh / cordoned
# rotation is required to fully retag the fleet (or an out-of-band
# `aws ec2 create-tags` for the existing instance IDs).
#
# `for_each` keys are tag names — strings sourced from
# `module.name.tags` / `var.tags`, all plan-time-known. The ASG name
# itself is apply-time-known (`aws_eks_node_group.this.resources[0]
# .autoscaling_groups[0].name`) but only flows into the resource's
# attributes, not its for_each key, so the lint-foreach-unknown-keys
# tripwire is satisfied. EKS managed-node-group ASGs always emit
# `resources[0].autoscaling_groups[0]` (one ASG per node group).
resource "aws_autoscaling_group_tag" "node_tags" {
  for_each = merge(local.common_tags, var.tags)

  autoscaling_group_name = aws_eks_node_group.this.resources[0].autoscaling_groups[0].name

  tag {
    key                 = each.key
    value               = each.value
    propagate_at_launch = true
  }
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

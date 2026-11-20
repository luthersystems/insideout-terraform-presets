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
  subcomponent   = "eks"
  resource       = "eks"

  # AWS IAM name_prefix is limited to 38 characters. The upstream
  # terraform-aws-modules/eks appends suffixes like "-cluster-" (9 chars)
  # and "-eks-auto-" (10 chars) as name_prefix values, so the base
  # cluster name must be ≤ 28 chars to stay within the limit.
  max_length = 28
}

locals {
  cluster_name = module.name.name
  common_tags  = merge(module.name.tags, var.tags)

  # ---------------------------------------------------------------------------
  # Addon version maps — pinned per Kubernetes minor version.
  # Override any individual addon version via var.addon_*_version.
  # ---------------------------------------------------------------------------
  vpc_cni_versions = {
    "1.28" = "v1.17.1-eksbuild.1"
    "1.29" = "v1.18.2-eksbuild.1"
    "1.30" = "v1.18.3-eksbuild.1"
    "1.31" = "v1.18.3-eksbuild.3"
    "1.32" = "v1.19.2-eksbuild.1"
    "1.33" = "v1.19.5-eksbuild.3"
  }

  kube_proxy_versions = {
    "1.28" = "v1.28.1-eksbuild.1"
    "1.29" = "v1.29.0-eksbuild.2"
    "1.30" = "v1.30.0-eksbuild.3"
    "1.31" = "v1.31.0-eksbuild.5"
    "1.32" = "v1.32.0-eksbuild.2"
    "1.33" = "v1.33.0-eksbuild.2"
  }

  coredns_versions = {
    "1.28" = "v1.10.1-eksbuild.4"
    "1.29" = "v1.11.1-eksbuild.4"
    "1.30" = "v1.11.1-eksbuild.9"
    "1.31" = "v1.11.3-eksbuild.1"
    "1.32" = "v1.11.4-eksbuild.2"
    "1.33" = "v1.12.1-eksbuild.2"
  }

  ebs_csi_versions = {
    "1.28" = "v1.28.0-eksbuild.1"
    "1.29" = "v1.31.0-eksbuild.1"
    "1.30" = "v1.31.0-eksbuild.1"
    "1.31" = "v1.35.0-eksbuild.1"
    "1.32" = "v1.45.0-eksbuild.2"
    "1.33" = "v1.45.0-eksbuild.2"
  }

  # Effective versions: user override takes precedence over version map lookup.
  effective_vpc_cni_version    = coalesce(var.addon_vpc_cni_version, local.vpc_cni_versions[var.cluster_version])
  effective_kube_proxy_version = coalesce(var.addon_kube_proxy_version, local.kube_proxy_versions[var.cluster_version])
  effective_coredns_version    = coalesce(var.addon_coredns_version, local.coredns_versions[var.cluster_version])
  effective_ebs_csi_version    = coalesce(var.addon_ebs_csi_version, local.ebs_csi_versions[var.cluster_version])

  # EBS CSI driver configuration values.
  ebs_csi_configuration_values = var.enable_ebs_csi_volume_modification ? jsonencode({
    controller = {
      volumeModificationFeature = { enabled = true }
    }
  }) : null

  # ---------------------------------------------------------------------------
  # Addon map — built conditionally so toggles actually remove addons.
  # Pod identity for EBS CSI is declared inline to fix ordering: the upstream
  # module creates the association BEFORE the addon, ensuring IAM permissions
  # are in place when the driver pods start.
  #
  # Every entry sets resolve_conflicts_on_create = "OVERWRITE" and
  # resolve_conflicts_on_update = "OVERWRITE" (issue #223). The upstream
  # module's default of "NONE" wedges every subsequent apply with HTTP 409
  # ResourceInUseException whenever a partial prior apply leaves an addon
  # created in EKS but not persisted in Terraform state — apply-time
  # crash, network blip, downstream resource error, user Ctrl-C, etc. all
  # produce the same orphan. Production session sess_v2_hrbS5zpRBk51 was
  # wedged on this exact path. OVERWRITE on both create and update is the
  # right choice here because addon versions are pinned in
  # local.*_versions and configuration_values are explicit — the config
  # IS the source of truth, we want TF to reconcile to match config
  # rather than preserve whatever the orphan left behind.
  #
  # Diverges from aws/eks_nodegroup's amazon-cloudwatch-observability
  # addon (PR #238), which uses OVERWRITE/PRESERVE because it ships a
  # CloudWatch agent + fluent-bit DaemonSets that operators may
  # hand-tune in-cluster. The five core addons here have no such
  # customization story.
  # ---------------------------------------------------------------------------
  addons = merge(
    {
      vpc-cni = {
        addon_version               = local.effective_vpc_cni_version
        before_compute              = true
        most_recent                 = false
        resolve_conflicts_on_create = "OVERWRITE"
        resolve_conflicts_on_update = "OVERWRITE"
      }
      eks-pod-identity-agent = {
        before_compute              = true
        resolve_conflicts_on_create = "OVERWRITE"
        resolve_conflicts_on_update = "OVERWRITE"
      }
    },
    var.enable_kube_proxy ? {
      kube-proxy = {
        addon_version               = local.effective_kube_proxy_version
        most_recent                 = false
        resolve_conflicts_on_create = "OVERWRITE"
        resolve_conflicts_on_update = "OVERWRITE"
      }
    } : {},
    var.enable_coredns ? {
      coredns = {
        addon_version               = local.effective_coredns_version
        most_recent                 = false
        resolve_conflicts_on_create = "OVERWRITE"
        resolve_conflicts_on_update = "OVERWRITE"
      }
    } : {},
    var.enable_ebs_csi_driver ? {
      aws-ebs-csi-driver = {
        addon_version               = local.effective_ebs_csi_version
        most_recent                 = false
        configuration_values        = local.ebs_csi_configuration_values
        resolve_conflicts_on_create = "OVERWRITE"
        resolve_conflicts_on_update = "OVERWRITE"
        pod_identity_association = [{
          role_arn        = aws_iam_role.ebs_csi[0].arn
          service_account = "ebs-csi-controller-sa"
        }]
      }
    } : {},
  )
}

data "aws_caller_identity" "current" {}
data "aws_region" "current" {}

data "aws_iam_policy_document" "ebs_csi_pod_identity_trust" {
  count = var.enable_ebs_csi_driver ? 1 : 0

  statement {
    effect = "Allow"

    actions = [
      "sts:AssumeRole",
      "sts:TagSession"
    ]

    principals {
      type        = "Service"
      identifiers = ["pods.eks.amazonaws.com"]
    }

    condition {
      test     = "StringEquals"
      variable = "aws:SourceAccount"
      values   = [data.aws_caller_identity.current.account_id]
    }

    condition {
      test     = "ArnLike"
      variable = "aws:SourceArn"
      values = [
        "arn:aws:eks:${data.aws_region.current.region}:${data.aws_caller_identity.current.account_id}:cluster/${local.cluster_name}"
      ]
    }
  }
}

resource "aws_iam_role" "ebs_csi" {
  count              = var.enable_ebs_csi_driver ? 1 : 0
  name               = "${local.cluster_name}-ebs-csi"
  assume_role_policy = data.aws_iam_policy_document.ebs_csi_pod_identity_trust[0].json
  tags               = local.common_tags

  path                 = try(var.iam_scope.path, null)
  permissions_boundary = try(var.iam_scope.permissions_boundary, null)
}

resource "aws_iam_role_policy_attachment" "ebs_csi" {
  count      = var.enable_ebs_csi_driver ? 1 : 0
  role       = aws_iam_role.ebs_csi[0].name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonEBSCSIDriverPolicy"
}

module "eks" {
  source = "terraform-aws-modules/eks/aws"
  # Pinned exactly: composed archives pin hashicorp/aws to the mars
  # provider-mirror bake (= 6.52.0, pkg/composer/imported/provider_pins.go),
  # and a floating "~> 21.0" here resolved eks v21.25.0 the day it released,
  # which requires aws >= 6.59 — making the composed constraint set
  # unsatisfiable and failing every terraform init (CI go-test run
  # 32429710972 and customer deploys alike). v21.24.0 is the newest release
  # compatible with the 6.52.0 pin (requires aws >= 6.52). Bump this pin
  # together with the base provider pin / mars bake.
  version = "21.24.0"

  name               = local.cluster_name
  kubernetes_version = var.cluster_version

  addons = local.addons

  addons_timeouts = var.addons_timeouts

  endpoint_public_access  = var.eks_public_control_plane
  endpoint_private_access = true

  enabled_log_types = var.cluster_enabled_log_types

  vpc_id                   = var.vpc_id
  subnet_ids               = var.private_subnet_ids
  control_plane_subnet_ids = var.private_subnet_ids

  enable_cluster_creator_admin_permissions = true

  # The scoped deploy policy allows IAM writes only under the project's IAM
  # path, and creates roles only with the workload boundary
  # (sandbox-infrastructure-template#147). null keeps the module defaults.
  iam_role_path                      = try(var.iam_scope.path, null)
  iam_role_permissions_boundary      = try(var.iam_scope.permissions_boundary, null)
  encryption_policy_path             = try(var.iam_scope.path, null)
  node_iam_role_path                 = try(var.iam_scope.path, null)
  node_iam_role_permissions_boundary = try(var.iam_scope.permissions_boundary, null)

  tags = local.common_tags
}

# -----------------------------------------------------------------------------
# Container Insights log groups
# -----------------------------------------------------------------------------
# aws/eks_nodegroup installs the amazon-cloudwatch-observability addon (on by
# default). Its CloudWatch agent and fluent-bit write to
# /aws/containerinsights/<cluster>/{application,dataplane,host,performance}
# and make those groups when they do not exist. Terraform did not own them,
# so a destroy left them, with their data and storage cost, and they never
# expired (#957).
#
# They are here, not in aws/eks_nodegroup, because the node group gets the
# cluster name from module.eks, which is unknown at plan time on a new
# stack. The existence check below needs the name at plan time; this
# preset has it (local.cluster_name). output.cluster_name depends on these
# groups, so the node group and its addon are made after them and
# destroyed before them, and no agent is left to make a group again.
#
# A stack deployed before #957 already has groups that the addon made.
# Making one again fails with ResourceAlreadyExistsException, and an
# import {} block is allowed only in the root module (#199). So the preset
# reads the groups that exist and makes a group only when it does not exist
# or has the owner tag. The owner tag is what keeps a group in the plan
# after the first apply; without it the next plan would see the group
# exists and delete it. A group that the addon made stays as it was.
locals {
  log_group_owner_tag = { "insideout:log-group-owner" = "terraform" }

  container_insights_log_group_names = var.container_insights_log_groups ? toset([
    for t in ["application", "dataplane", "host", "performance"] : "/aws/containerinsights/${local.cluster_name}/${t}"
  ]) : toset([])

  container_insights_log_groups_existing = setintersection(
    local.container_insights_log_group_names,
    toset(flatten([for d in data.aws_cloudwatch_log_groups.container_insights_existing : d.log_group_names])),
  )

  # try() reads the tag only for a group that exists (|| does not
  # short-circuit).
  container_insights_log_groups_owned = toset([
    for n in local.container_insights_log_group_names : n
    if !contains(local.container_insights_log_groups_existing, n) || try(data.aws_cloudwatch_log_group.container_insights_existing[n].tags["insideout:log-group-owner"], "") == "terraform"
  ])
}

data "aws_cloudwatch_log_groups" "container_insights_existing" {
  count                 = var.container_insights_log_groups ? 1 : 0
  log_group_name_prefix = "/aws/containerinsights/${local.cluster_name}/"
}

data "aws_cloudwatch_log_group" "container_insights_existing" {
  for_each = local.container_insights_log_groups_existing
  name     = each.key
}

resource "aws_cloudwatch_log_group" "container_insights" {
  for_each          = local.container_insights_log_groups_owned
  name              = each.key
  retention_in_days = var.container_insights_log_retention_days
  tags              = merge(local.common_tags, local.log_group_owner_tag)
}

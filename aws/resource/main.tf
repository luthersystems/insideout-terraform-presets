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
  # ---------------------------------------------------------------------------
  addons = merge(
    {
      vpc-cni = {
        addon_version  = local.effective_vpc_cni_version
        before_compute = true
        most_recent    = false
      }
      eks-pod-identity-agent = {
        before_compute = true
      }
    },
    var.enable_kube_proxy ? {
      kube-proxy = {
        addon_version = local.effective_kube_proxy_version
        most_recent   = false
      }
    } : {},
    var.enable_coredns ? {
      coredns = {
        addon_version = local.effective_coredns_version
        most_recent   = false
      }
    } : {},
    var.enable_ebs_csi_driver ? {
      aws-ebs-csi-driver = {
        addon_version        = local.effective_ebs_csi_version
        most_recent          = false
        configuration_values = local.ebs_csi_configuration_values
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
}

resource "aws_iam_role_policy_attachment" "ebs_csi" {
  count      = var.enable_ebs_csi_driver ? 1 : 0
  role       = aws_iam_role.ebs_csi[0].name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonEBSCSIDriverPolicy"
}

module "eks" {
  source  = "terraform-aws-modules/eks/aws"
  version = "~> 21.0"

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

  tags = local.common_tags
}

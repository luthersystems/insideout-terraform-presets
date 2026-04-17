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
  subcomponent   = "os"
  resource       = "os"
}

locals {
  is_serverless   = var.deployment_type == "serverless"
  collection_name = "${var.project}-search"
}

resource "aws_security_group" "opensearch" {
  count       = var.deployment_type == "managed" ? 1 : 0
  name        = "${module.name.name}-sg"
  description = "Security group for OpenSearch domain"
  vpc_id      = var.vpc_id

  ingress {
    from_port   = 443
    to_port     = 443
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"] # Should be restricted in production
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = merge(module.name.tags, { Name = "${module.name.name}-sg" }, var.tags)
}

# VPC-mode managed domains require the account-scoped service-linked role
# AWSServiceRoleForAmazonOpenSearchService. AWS only auto-creates it on first
# console use, so Terraform-only deploys into a fresh account fail without
# it. We probe for the role and create it only when absent, keeping this
# idempotent across accounts that already have it from a previous deploy.
data "aws_iam_roles" "opensearch_slr" {
  count       = var.deployment_type == "managed" ? 1 : 0
  name_regex  = "^AWSServiceRoleForAmazonOpenSearchService$"
  path_prefix = "/aws-service-role/opensearchservice.amazonaws.com/"
}

resource "aws_iam_service_linked_role" "opensearch" {
  count            = var.deployment_type == "managed" && length(data.aws_iam_roles.opensearch_slr) > 0 && length(data.aws_iam_roles.opensearch_slr[0].names) == 0 ? 1 : 0
  aws_service_name = "opensearchservice.amazonaws.com"
  description      = "Service-linked role for Amazon OpenSearch Service VPC access"
}

resource "aws_opensearch_domain" "managed" {
  count      = var.deployment_type == "managed" ? 1 : 0
  depends_on = [aws_iam_service_linked_role.opensearch]

  # OpenSearch domain names limited to 28 chars — use var.project
  domain_name    = "${var.project}-search"
  engine_version = "OpenSearch_2.11"

  cluster_config {
    instance_type          = var.instance_type
    instance_count         = var.multi_az ? 2 : 1
    zone_awareness_enabled = var.multi_az
  }

  ebs_options {
    ebs_enabled = true
    volume_size = tonumber(replace(var.storage_size, "GB", ""))
    volume_type = "gp3"
  }

  vpc_options {
    subnet_ids         = [var.subnet_ids[0]]
    security_group_ids = [aws_security_group.opensearch[0].id]
  }

  tags = merge(module.name.tags, { Domain = module.name.name }, var.tags)
}

# --- AOSS serverless path --------------------------------------------------
#
# Provisions an empty vector collection + its required encryption and
# network security policies. Data-access policies and vector indexes are
# intentionally NOT created here: they depend on who is consuming the
# collection (e.g. a Bedrock KB role, the application's ingestion role),
# which embedding model / dimension the application uses, and are therefore
# an application-layer concern. The application creates its own data-access
# policy and vector index after this infra exists.

# AOSS service-linked role. AWS auto-creates it on first collection create
# in most accounts, but fresh accounts and tight IAM propagation windows can
# race, so we preemptively probe and create if missing. Same idiom as the
# managed-OpenSearch SLR above.
data "aws_iam_roles" "aoss_slr" {
  count       = local.is_serverless ? 1 : 0
  name_regex  = "^AWSServiceRoleForAmazonOpenSearchServerless$"
  path_prefix = "/aws-service-role/observability.aoss.amazonaws.com/"
}

resource "aws_iam_service_linked_role" "aoss" {
  count            = local.is_serverless && length(data.aws_iam_roles.aoss_slr) > 0 && length(data.aws_iam_roles.aoss_slr[0].names) == 0 ? 1 : 0
  aws_service_name = "observability.aoss.amazonaws.com"
  description      = "Service-linked role for Amazon OpenSearch Serverless"
}

resource "aws_opensearchserverless_security_policy" "encryption" {
  count = local.is_serverless ? 1 : 0
  name  = "${var.project}-enc"
  type  = "encryption"
  # AOSS expects exactly one of AWSOwnedKey or KmsARN — emit only the
  # active field, not both with a null. Otherwise AOSS rejects the policy.
  policy = jsonencode(merge(
    {
      Rules = [
        {
          ResourceType = "collection"
          Resource     = ["collection/${local.collection_name}"]
        }
      ]
    },
    var.kms_key_arn == null ? { AWSOwnedKey = true } : { KmsARN = var.kms_key_arn }
  ))
}

resource "aws_opensearchserverless_security_policy" "network" {
  count = local.is_serverless ? 1 : 0
  name  = "${var.project}-net"
  type  = "network"
  policy = jsonencode([
    {
      Rules = [
        {
          ResourceType = "collection"
          Resource     = ["collection/${local.collection_name}"]
        },
        {
          ResourceType = "dashboard"
          Resource     = ["collection/${local.collection_name}"]
        }
      ]
      AllowFromPublic = var.allow_public_access
    }
  ])
}

resource "aws_opensearchserverless_collection" "serverless" {
  count = local.is_serverless ? 1 : 0

  # OpenSearch collection names limited to 32 chars — use var.project
  name = local.collection_name
  type = "VECTORSEARCH"

  tags = merge(module.name.tags, { Name = module.name.name }, var.tags)

  depends_on = [
    aws_iam_service_linked_role.aoss,
    aws_opensearchserverless_security_policy.encryption,
    aws_opensearchserverless_security_policy.network,
  ]
}

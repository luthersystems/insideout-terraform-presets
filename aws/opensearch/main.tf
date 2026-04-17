terraform {
  required_version = ">= 1.5"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 6.0"
    }
    opensearch = {
      source  = "opensearch-project/opensearch"
      version = "~> 2.3"
    }
    time = {
      source  = "hashicorp/time"
      version = ">= 0.9"
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
  is_serverless     = var.deployment_type == "serverless"
  collection_name   = "${var.project}-search"
  emit_data_access  = local.is_serverless && length(var.data_access_principal_arns) > 0
  create_vector_idx = local.is_serverless && var.create_bedrock_vector_index
  # AOSS data-access policies reject sts assumed-role session ARNs; resolve
  # the caller's underlying role via aws_iam_session_context so the
  # terraform runner can itself PUT the vector index.
  data_access_principals = local.emit_data_access ? distinct(concat(
    var.data_access_principal_arns,
    [data.aws_iam_session_context.current[0].issuer_arn]
  )) : []
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
# AOSS requires encryption + network security policies *before* the collection
# can be created. Data-access policies and the Bedrock default vector index
# are created conditionally based on caller-supplied principals and the
# create_bedrock_vector_index flag.

data "aws_caller_identity" "current" {
  count = local.emit_data_access ? 1 : 0
}

data "aws_iam_session_context" "current" {
  count = local.emit_data_access ? 1 : 0
  arn   = data.aws_caller_identity.current[0].arn
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
    aws_opensearchserverless_security_policy.encryption,
    aws_opensearchserverless_security_policy.network,
  ]
}

resource "aws_opensearchserverless_access_policy" "data" {
  count = local.emit_data_access ? 1 : 0
  name  = "${var.project}-data"
  type  = "data"
  # Scoped to the exact permissions Bedrock KB needs plus index
  # create/update/delete so the Terraform caller can manage the vector
  # index. Matches the AWS-published reference policy for Bedrock + AOSS.
  policy = jsonencode([
    {
      Rules = [
        {
          ResourceType = "collection"
          Resource     = ["collection/${local.collection_name}"]
          Permission = [
            "aoss:DescribeCollectionItems",
            "aoss:CreateCollectionItems",
            "aoss:UpdateCollectionItems",
          ]
        },
        {
          ResourceType = "index"
          Resource     = ["index/${local.collection_name}/*"]
          Permission = [
            "aoss:ReadDocument",
            "aoss:WriteDocument",
            "aoss:CreateIndex",
            "aoss:DescribeIndex",
            "aoss:UpdateIndex",
            "aoss:DeleteIndex",
          ]
        }
      ]
      Principal = local.data_access_principals
    }
  ])
}

# AOSS data-access policies propagate asynchronously; a PUT against the
# collection immediately after policy creation returns 403 with some
# regularity. 45s is the empirically safe floor.
resource "time_sleep" "aoss_policy_propagation" {
  count           = local.create_vector_idx ? 1 : 0
  create_duration = "45s"
  depends_on = [
    aws_opensearchserverless_access_policy.data,
    aws_opensearchserverless_collection.serverless,
  ]
}

# The opensearch provider must be configured even when serverless is
# disabled (required_providers is module-global). The url points at a
# placeholder in that case; no opensearch_* resource is ever created
# unless create_vector_idx is true, so the placeholder is never dialled.
provider "opensearch" {
  url               = local.is_serverless && length(aws_opensearchserverless_collection.serverless) > 0 ? aws_opensearchserverless_collection.serverless[0].collection_endpoint : "https://placeholder.invalid"
  aws_region        = var.region
  sign_aws_requests = true
  healthcheck       = false
}

resource "opensearch_index" "bedrock_default" {
  count = local.create_vector_idx ? 1 : 0
  name  = "bedrock-knowledge-base-default-index"

  # AOSS manages replica count internally and does not accept
  # number_of_replicas on index settings; shards and knn only.
  number_of_shards = "2"
  index_knn        = true

  lifecycle {
    precondition {
      condition     = length(var.data_access_principal_arns) > 0
      error_message = "create_bedrock_vector_index requires data_access_principal_arns to be non-empty. Pass the Bedrock KB role ARN (e.g. [module.bedrock.role_arn]) so the AOSS data-access policy grants it — and the Terraform runner — aoss:* on the index."
    }
  }

  mappings = jsonencode({
    properties = {
      "bedrock-knowledge-base-default-vector" = {
        type      = "knn_vector"
        dimension = var.vector_embedding_dimension
        method = {
          name       = "hnsw"
          engine     = "faiss"
          space_type = "l2"
          parameters = {
            m               = 16
            ef_construction = 512
          }
        }
      }
      "AMAZON_BEDROCK_TEXT_CHUNK" = {
        type = "text"
      }
      "AMAZON_BEDROCK_METADATA" = {
        type  = "text"
        index = false
      }
    }
  })

  depends_on = [time_sleep.aoss_policy_propagation]
}

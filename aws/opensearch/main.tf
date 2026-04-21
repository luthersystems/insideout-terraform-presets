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

  # Managed-mode log types published to CloudWatch. AUDIT_LOGS is deliberately
  # excluded: it requires fine-grained access control (advanced_security_options
  # with a master user), which this module does not currently enable. Tracked
  # as a separate follow-up on #95.
  opensearch_log_types = var.deployment_type == "managed" ? ["INDEX_SLOW_LOGS", "SEARCH_SLOW_LOGS", "ES_APPLICATION_LOGS"] : []
}

resource "aws_security_group" "opensearch" {
  count       = var.deployment_type == "managed" ? 1 : 0
  name        = "${module.name.name}-sg"
  description = "Security group for OpenSearch domain"
  vpc_id      = var.vpc_id
  lifecycle {
    precondition {
      condition     = var.vpc_id != null && length(var.subnet_ids) > 0
      error_message = "Managed OpenSearch deployment requires vpc_id and at least one subnet_id. Either set deployment_type = \"serverless\" or pass vpc_id and subnet_ids."
    }
  }

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

# `data.aws_iam_roles.opensearch_slr` is itself count-conditional, so it is a
# tuple that can be empty. Terraform analyses the `[0]` access statically and
# rejects it when the tuple is empty — `&&` short-circuit does not help. Wrap
# the index in try() and default to 1 ("names already present, do nothing") so
# we never race to create an SLR that may already exist.
resource "aws_iam_service_linked_role" "opensearch" {
  count            = var.deployment_type == "managed" && try(length(data.aws_iam_roles.opensearch_slr[0].names), 1) == 0 ? 1 : 0
  aws_service_name = "opensearchservice.amazonaws.com"
  description      = "Service-linked role for Amazon OpenSearch Service VPC access"
}

# CloudWatch log groups for slow/application log publishing. AWS does not
# create these implicitly; when log_publishing_options points at a non-
# existent log group, the domain-create call fails with AccessDeniedException.
resource "aws_cloudwatch_log_group" "opensearch" {
  for_each          = toset(local.opensearch_log_types)
  name              = "/aws/opensearch/${var.project}-search/${lower(replace(each.value, "_", "-"))}"
  retention_in_days = var.log_retention_days
  tags              = merge(module.name.tags, { Name = "${var.project}-search-${lower(replace(each.value, "_", "-"))}" }, var.tags)
}

# CloudWatch account-scoped resource policy authorising the OpenSearch service
# to write to the log groups above. One policy per project, scoped to the
# module's log-group prefix — keeps below the 10-policies-per-account limit
# when multiple domains share an account.
data "aws_iam_policy_document" "opensearch_logs" {
  count = var.deployment_type == "managed" ? 1 : 0
  statement {
    effect = "Allow"
    principals {
      type        = "Service"
      identifiers = ["es.amazonaws.com"]
    }
    actions = [
      "logs:PutLogEvents",
      "logs:CreateLogStream",
    ]
    resources = ["arn:aws:logs:*:*:log-group:/aws/opensearch/${var.project}-search/*:*"]
  }
}

resource "aws_cloudwatch_log_resource_policy" "opensearch_logs" {
  count           = var.deployment_type == "managed" ? 1 : 0
  policy_name     = "${var.project}-opensearch-logs"
  policy_document = data.aws_iam_policy_document.opensearch_logs[0].json
}

resource "aws_opensearch_domain" "managed" {
  count = var.deployment_type == "managed" ? 1 : 0
  depends_on = [
    aws_iam_service_linked_role.opensearch,
    aws_cloudwatch_log_resource_policy.opensearch_logs,
  ]

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

  dynamic "log_publishing_options" {
    for_each = toset(local.opensearch_log_types)
    content {
      log_type                 = log_publishing_options.value
      cloudwatch_log_group_arn = aws_cloudwatch_log_group.opensearch[log_publishing_options.value].arn
      enabled                  = true
    }
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
#
# Note the service-role path: "observability.aoss.amazonaws.com" (not plain
# "aoss.amazonaws.com"). AWS files the AOSS SLR under the observability
# service namespace — counterintuitive but correct. Verify by inspecting an
# account that's already used AOSS: the role lives at
# /aws-service-role/observability.aoss.amazonaws.com/AWSServiceRoleForAmazonOpenSearchServerless
data "aws_iam_roles" "aoss_slr" {
  count       = local.is_serverless ? 1 : 0
  name_regex  = "^AWSServiceRoleForAmazonOpenSearchServerless$"
  path_prefix = "/aws-service-role/observability.aoss.amazonaws.com/"
}

# Same empty-tuple hazard as aws_iam_service_linked_role.opensearch above —
# see the comment on that resource for rationale.
resource "aws_iam_service_linked_role" "aoss" {
  count            = local.is_serverless && try(length(data.aws_iam_roles.aoss_slr[0].names), 1) == 0 ? 1 : 0
  aws_service_name = "observability.aoss.amazonaws.com"
  description      = "Service-linked role for Amazon OpenSearch Serverless"
}

resource "aws_opensearchserverless_security_policy" "encryption" {
  count = local.is_serverless ? 1 : 0
  name  = "${var.project}-enc"
  type  = "encryption"
  # AOSS expects exactly one of AWSOwnedKey (bool) or KmsARN (string).
  # jsonencode() is applied inside each arm so the ternary unifies on
  # string, not on a map value type. An earlier merge()-based version
  # forced HCL to unify bool with string across the two arms and emitted
  # AWSOwnedKey as the string "true", which AOSS rejects.
  policy = var.kms_key_arn == null ? jsonencode({
    Rules = [
      {
        ResourceType = "collection"
        Resource     = ["collection/${local.collection_name}"]
      }
    ]
    AWSOwnedKey = true
    }) : jsonencode({
    Rules = [
      {
        ResourceType = "collection"
        Resource     = ["collection/${local.collection_name}"]
      }
    ]
    KmsARN = var.kms_key_arn
  })
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

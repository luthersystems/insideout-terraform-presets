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
  subcomponent   = "br"
  resource       = "br"
}

# Used by the IAM role trust policies below to scope service-principal trust
# to this account only — AWS's documented mitigation against the
# cross-account confused-deputy attack on bedrock.amazonaws.com.
data "aws_caller_identity" "current" {}

resource "aws_iam_role" "bedrock_kb" {
  name = "${var.project}-bedrock-role"

  # Scoping the service trust to aws:SourceAccount = this account closes the
  # cross-account confused-deputy hole on bedrock.amazonaws.com (an attacker
  # in another account could otherwise coax Bedrock into assuming this role
  # from their context). The condition is satisfied automatically by every
  # legitimate in-account caller, so it is backward-compatible.
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Action = "sts:AssumeRole"
        Effect = "Allow"
        Principal = {
          Service = "bedrock.amazonaws.com"
        }
        Condition = {
          StringEquals = {
            "aws:SourceAccount" = data.aws_caller_identity.current.account_id
          }
        }
      }
    ]
  })

  tags = merge(module.name.tags, var.tags)
}

resource "aws_iam_role_policy" "bedrock_kb" {
  name = "${var.project}-bedrock-policy"
  role = aws_iam_role.bedrock_kb.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Action = [
          "bedrock:InvokeModel"
        ]
        Effect = "Allow"
        # KB ingestion calls the embedding model; downstream consumers
        # of the KB typically invoke the chat model through the same role.
        Resource = [
          "arn:aws:bedrock:${var.region}::foundation-model/${var.model_id}",
          "arn:aws:bedrock:${var.region}::foundation-model/${var.embedding_model_id}",
        ]
      },
      {
        Action = [
          "s3:ListBucket",
          "s3:GetObject"
        ]
        Effect = "Allow"
        Resource = [
          var.s3_bucket_arn,
          "${var.s3_bucket_arn}/*"
        ]
      },
      {
        Action   = ["aoss:APIAccessAll"]
        Effect   = "Allow"
        Resource = var.opensearch_collection_arn
      }
    ]
  })
}

# The Bedrock Knowledge Base (aws_bedrockagent_knowledge_base) and its S3
# data source are intentionally NOT managed by this module. Creating the KB
# at terraform time requires (1) a pre-existing AOSS vector index with the
# k-NN field mapping Bedrock expects keyed to a chosen embedding model and
# dimension, and (2) resolving assumed-role session ARNs to their underlying
# roles. Both are application-layer concerns — the application that ingests
# data into the KB is the right place to create the KB and the vector index,
# because only it knows which embedding model / dimension / field names it
# needs. This module provisions the IAM role + AOSS data-access policy +
# invocation logging + guardrail so the application has a usable, observable
# substrate to call CreateKnowledgeBase and StartIngestionJob against.

# --- AOSS data-access policy -----------------------------------------------
#
# AOSS access has two independent layers: IAM (granted by the inline policy
# above) and a collection-side data-access policy. Bedrock KB creation,
# ingestion, and query all silently 403 without both. This block creates the
# AOSS-side half granting the bedrock role + any aoss_additional_principal_arns
# (typically the terraform runner that creates the vector index, plus the
# application's ingestion role) data-plane access on the collection.
#
# Subtle: AOSS data-access policies match exact role ARNs and do NOT resolve
# assumed-role sessions back to their underlying role, unlike IAM. If the
# caller uses sts:AssumeRole, pass the underlying role ARN here, not the
# session ARN — the variable validation enforces this.
#
# The AOSS policy-name limit is 32 chars; "${project}-br-data" caps project at
# 24 which is well within the project-name limits already enforced upstream
# by the opensearch module.
resource "aws_opensearchserverless_access_policy" "bedrock" {
  count = var.opensearch_collection_name == null ? 0 : 1
  name  = "${var.project}-br-data"
  type  = "data"
  policy = jsonencode([
    {
      Description = "Bedrock KB role + additional principals data-plane access on ${var.opensearch_collection_name}"
      Rules = [
        {
          ResourceType = "collection"
          Resource     = ["collection/${var.opensearch_collection_name}"]
          Permission = [
            "aoss:DescribeCollectionItems",
            "aoss:CreateCollectionItems",
            "aoss:UpdateCollectionItems",
          ]
        },
        {
          ResourceType = "index"
          Resource     = ["index/${var.opensearch_collection_name}/*"]
          Permission = [
            "aoss:CreateIndex",
            "aoss:DescribeIndex",
            "aoss:ReadDocument",
            "aoss:WriteDocument",
            "aoss:UpdateIndex",
            "aoss:DeleteIndex",
          ]
        }
      ]
      Principal = concat(
        [aws_iam_role.bedrock_kb.arn],
        var.aoss_additional_principal_arns,
      )
    }
  ])
}

# --- Invocation logging ----------------------------------------------------
#
# aws_bedrock_model_invocation_logging_configuration is an account+region
# singleton. Enabling it here logs every Bedrock InvokeModel call across the
# account into the log group below. If multiple stacks in the same account
# enable it, the last apply wins and earlier stacks lose their logging
# silently — opt in deliberately via enable_invocation_logging.

resource "aws_cloudwatch_log_group" "invocations" {
  count             = var.enable_invocation_logging ? 1 : 0
  name              = "/aws/bedrock/${var.project}-invocations"
  retention_in_days = var.invocation_log_retention_days
  tags              = merge(module.name.tags, var.tags)
}

resource "aws_iam_role" "invocation_logging" {
  count = var.enable_invocation_logging ? 1 : 0
  name  = "${var.project}-bedrock-logging-role"

  # Same confused-deputy guard as bedrock_kb above.
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Action = "sts:AssumeRole"
      Effect = "Allow"
      Principal = {
        Service = "bedrock.amazonaws.com"
      }
      Condition = {
        StringEquals = {
          "aws:SourceAccount" = data.aws_caller_identity.current.account_id
        }
      }
    }]
  })

  tags = merge(module.name.tags, var.tags)
}

resource "aws_iam_role_policy" "invocation_logging" {
  count = var.enable_invocation_logging ? 1 : 0
  name  = "${var.project}-bedrock-logging-policy"
  role  = aws_iam_role.invocation_logging[0].id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Action = [
        "logs:CreateLogStream",
        "logs:PutLogEvents",
      ]
      Resource = [
        aws_cloudwatch_log_group.invocations[0].arn,
        "${aws_cloudwatch_log_group.invocations[0].arn}:log-stream:*",
      ]
    }]
  })
}

resource "aws_bedrock_model_invocation_logging_configuration" "this" {
  count      = var.enable_invocation_logging ? 1 : 0
  depends_on = [aws_iam_role_policy.invocation_logging]

  logging_config {
    embedding_data_delivery_enabled = var.log_embedding_data
    image_data_delivery_enabled     = var.log_image_data
    text_data_delivery_enabled      = var.log_text_data
    cloudwatch_config {
      log_group_name = aws_cloudwatch_log_group.invocations[0].name
      role_arn       = aws_iam_role.invocation_logging[0].arn
    }
  }
}

# --- Guardrail -------------------------------------------------------------
#
# A reusable content-moderation policy. The application opts in by passing
# guardrail_id + guardrail_version to InvokeModel/Converse — this module only
# defines the policy, it does not bind it to any specific model.
#
# Defaults are chosen to be broadly applicable: MEDIUM strength on the
# universal content categories, PII anonymisation on the most sensitive
# categories, and PROMPT_ATTACK pinned to HIGH on input as cheap insurance
# against jailbreak attempts (the only output_strength AWS accepts for
# PROMPT_ATTACK is NONE, hence its separate filters_config block).

locals {
  content_filter_categories = ["SEXUAL", "VIOLENCE", "HATE", "INSULTS", "MISCONDUCT"]
}

resource "aws_bedrock_guardrail" "this" {
  count                     = var.enable_guardrail ? 1 : 0
  name                      = "${var.project}-guardrail"
  description               = "InsideOut default guardrail for ${var.project} (${var.environment})."
  blocked_input_messaging   = var.guardrail_blocked_input_messaging
  blocked_outputs_messaging = var.guardrail_blocked_outputs_messaging
  kms_key_arn               = var.guardrail_kms_key_arn

  dynamic "content_policy_config" {
    for_each = var.guardrail_content_filter_strength == "NONE" ? [] : [1]
    content {
      dynamic "filters_config" {
        for_each = local.content_filter_categories
        content {
          type            = filters_config.value
          input_strength  = var.guardrail_content_filter_strength
          output_strength = var.guardrail_content_filter_strength
        }
      }
      filters_config {
        type            = "PROMPT_ATTACK"
        input_strength  = "HIGH"
        output_strength = "NONE"
      }
    }
  }

  dynamic "sensitive_information_policy_config" {
    for_each = var.guardrail_pii_action == "NONE" || length(var.guardrail_pii_entities) == 0 ? [] : [1]
    content {
      dynamic "pii_entities_config" {
        for_each = var.guardrail_pii_entities
        content {
          type   = pii_entities_config.value
          action = var.guardrail_pii_action
        }
      }
    }
  }

  dynamic "topic_policy_config" {
    for_each = length(var.guardrail_denied_topics) > 0 ? [1] : []
    content {
      dynamic "topics_config" {
        for_each = var.guardrail_denied_topics
        content {
          name       = topics_config.value.name
          definition = topics_config.value.definition
          examples   = topics_config.value.examples
          type       = "DENY"
        }
      }
    }
  }

  dynamic "word_policy_config" {
    for_each = length(var.guardrail_blocked_words) > 0 ? [1] : []
    content {
      dynamic "words_config" {
        for_each = var.guardrail_blocked_words
        content {
          text = words_config.value
        }
      }
    }
  }

  tags = merge(module.name.tags, var.tags)
}

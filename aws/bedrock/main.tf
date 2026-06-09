terraform {
  required_version = ">= 1.5"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 6.0"
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

  # Inline + managed policies are attached via aws_iam_role_policy /
  # aws_iam_role_policy_attachment siblings. The provider re-reads them onto
  # the role on refresh and drift-check flags them; ignore here.
  lifecycle {
    ignore_changes = [inline_policy, managed_policy_arns]
  }
}

# The role's inline policy is assembled from one always-on statement plus
# two optional ones. bedrock:InvokeModel is unconditional — it is the whole
# point of the role for the plain model-invocation use case, and keeps the
# policy non-empty (aws_iam_role_policy rejects an empty Statement list).
# The S3 and AOSS statements are appended only when their ARN is supplied:
# they are Knowledge Base concerns (an S3 data source to ingest from, an
# AOSS collection to grant data-plane access on) and have no purpose for a
# role that only invokes models.
locals {
  bedrock_invoke_statement = {
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
  }

  bedrock_s3_statements = var.s3_bucket_arn == null ? [] : [
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
    }
  ]

  bedrock_aoss_statements = var.opensearch_collection_arn == null ? [] : [
    {
      Action   = ["aoss:APIAccessAll"]
      Effect   = "Allow"
      Resource = var.opensearch_collection_arn
    }
  ]

  # When the KB uses the in-module S3 Vectors store, the KB service role needs
  # data-plane access on the vector bucket + index it was given. Granted only
  # for the s3vectors path; the opensearch path uses the aoss statement above.
  use_s3vectors = var.enable_knowledge_base && var.vector_store == "s3vectors"

  bedrock_s3vectors_statements = local.use_s3vectors ? [
    {
      Action = [
        "s3vectors:GetIndex",
        "s3vectors:QueryVectors",
        "s3vectors:GetVectors",
        "s3vectors:PutVectors",
        "s3vectors:ListVectors",
        "s3vectors:DeleteVectors",
      ]
      Effect = "Allow"
      Resource = [
        aws_s3vectors_vector_bucket.kb[0].vector_bucket_arn,
        aws_s3vectors_index.kb[0].index_arn,
      ]
    }
  ] : []
}

resource "aws_iam_role_policy" "bedrock_kb" {
  name = "${var.project}-bedrock-policy"
  role = aws_iam_role.bedrock_kb.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = concat(
      [local.bedrock_invoke_statement],
      local.bedrock_s3_statements,
      local.bedrock_aoss_statements,
      local.bedrock_s3vectors_statements,
    )
  })
}

# --- Knowledge Base + vector store -----------------------------------------
#
# When enable_knowledge_base is true this module provisions a real managed-RAG
# Knowledge Base: a vector store, the aws_bedrockagent_knowledge_base wired to
# the KB IAM role above, and an aws_bedrockagent_data_source pointed at the S3
# docs bucket. This reverses the earlier "KB is app-layer" decision (#757):
# with the S3 Vectors store the dimension/field-mapping bootstrapping that used
# to require an application is now a couple of plain terraform resources, so
# the whole RAG substrate composes declaratively.
#
# Two vector stores are supported:
#   - s3vectors (default): an aws_s3vectors_vector_bucket + aws_s3vectors_index
#     created here. Cheapest managed option, no cluster. Bedrock's s3_vectors
#     storage type does NOT need a field_mapping block — the index schema is
#     fixed by the s3vectors index itself.
#   - opensearch: the AOSS collection wired via opensearch_collection_arn. The
#     vector index (with the field mapping Bedrock expects) must already exist
#     on that collection; this module grants the data-access policy below.

locals {
  embedding_model_arn = "arn:aws:bedrock:${var.region}::foundation-model/${var.embedding_model_id}"

  # Field names Bedrock writes/reads on the AOSS vector index. These are the
  # conventional names the application's index-creation step must mirror.
  aoss_vector_field   = "bedrock-knowledge-base-default-vector"
  aoss_text_field     = "AMAZON_BEDROCK_TEXT_CHUNK"
  aoss_metadata_field = "AMAZON_BEDROCK_METADATA"
}

# S3 Vectors store (default). force_destroy defaults false so a destroy of a
# populated KB fails loud instead of silently dropping ingested vectors.
resource "aws_s3vectors_vector_bucket" "kb" {
  count              = local.use_s3vectors ? 1 : 0
  vector_bucket_name = "${var.project}-br-vectors"
  force_destroy      = var.knowledge_base_force_destroy
  tags               = merge(module.name.tags, var.tags)
}

resource "aws_s3vectors_index" "kb" {
  count              = local.use_s3vectors ? 1 : 0
  vector_bucket_name = aws_s3vectors_vector_bucket.kb[0].vector_bucket_name
  index_name         = "${var.project}-br-index"
  data_type          = "float32"
  dimension          = var.embedding_dimension
  # Cosine is the metric Bedrock's Titan embeddings are tuned for.
  distance_metric = "cosine"
  tags            = merge(module.name.tags, var.tags)

  lifecycle {
    # Cross-check the index dimension against the known Titan models so a wrong
    # pairing fails at plan time rather than producing a non-retrievable index.
    # Unknown/custom models skip the check (any in-range dimension is accepted).
    precondition {
      condition = (
        var.embedding_model_id == "amazon.titan-embed-text-v2:0" ? var.embedding_dimension == 1024 :
        var.embedding_model_id == "amazon.titan-embed-text-v1" ? var.embedding_dimension == 1536 :
        true
      )
      error_message = "embedding_dimension must match embedding_model_id: amazon.titan-embed-text-v2:0 → 1024, amazon.titan-embed-text-v1 → 1536."
    }
  }
}

# IAM is eventually consistent: the KB role/policy created above can take a few
# seconds to propagate before Bedrock's CreateKnowledgeBase will accept it,
# otherwise it 403s on the first apply. A short sleep between the policy and the
# KB resource turns a flaky first apply into a reliable one.
resource "time_sleep" "kb_iam_propagation" {
  count           = var.enable_knowledge_base ? 1 : 0
  depends_on      = [aws_iam_role_policy.bedrock_kb]
  create_duration = "20s"
}

resource "aws_bedrockagent_knowledge_base" "this" {
  count    = var.enable_knowledge_base ? 1 : 0
  name     = "${var.project}-kb"
  role_arn = aws_iam_role.bedrock_kb.arn

  knowledge_base_configuration {
    type = "VECTOR"
    vector_knowledge_base_configuration {
      embedding_model_arn = local.embedding_model_arn
    }
  }

  storage_configuration {
    type = local.use_s3vectors ? "S3_VECTORS" : "OPENSEARCH_SERVERLESS"

    dynamic "s3_vectors_configuration" {
      for_each = local.use_s3vectors ? [1] : []
      content {
        index_arn = aws_s3vectors_index.kb[0].index_arn
      }
    }

    dynamic "opensearch_serverless_configuration" {
      for_each = local.use_s3vectors ? [] : [1]
      content {
        collection_arn    = var.opensearch_collection_arn
        vector_index_name = "${var.project}-br-index"
        field_mapping {
          vector_field   = local.aoss_vector_field
          text_field     = local.aoss_text_field
          metadata_field = local.aoss_metadata_field
        }
      }
    }
  }

  tags = merge(module.name.tags, var.tags)

  # Wait for the KB role/policy to propagate, and (opensearch path) for the
  # AOSS data-access policy to exist before Bedrock validates index access.
  depends_on = [
    time_sleep.kb_iam_propagation,
    aws_opensearchserverless_access_policy.bedrock,
  ]

  lifecycle {
    # The S3 docs source is mandatory for any KB regardless of vector store.
    precondition {
      condition     = var.s3_bucket_arn != null
      error_message = "enable_knowledge_base=true requires s3_bucket_arn (the S3 docs source the Knowledge Base ingests from)."
    }
    # The opensearch store needs the AOSS collection ARN wired.
    precondition {
      condition     = var.vector_store != "opensearch" || var.opensearch_collection_arn != null
      error_message = "vector_store=opensearch requires opensearch_collection_arn to be set (wire from aws/opensearch.collection_arn)."
    }
  }
}

resource "aws_bedrockagent_data_source" "s3_docs" {
  count             = var.enable_knowledge_base ? 1 : 0
  knowledge_base_id = aws_bedrockagent_knowledge_base.this[0].id
  name              = "${var.project}-kb-s3"

  data_source_configuration {
    type = "S3"
    s3_configuration {
      bucket_arn         = var.s3_bucket_arn
      inclusion_prefixes = length(var.knowledge_base_inclusion_prefixes) > 0 ? var.knowledge_base_inclusion_prefixes : null
    }
  }
}

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

  # See bedrock_kb above.
  lifecycle {
    ignore_changes = [inline_policy, managed_policy_arns]
  }
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

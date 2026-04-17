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

resource "aws_iam_role" "bedrock_kb" {
  name = "${var.project}-bedrock-role"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Action = "sts:AssumeRole"
        Effect = "Allow"
        Principal = {
          Service = "bedrock.amazonaws.com"
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
# k-NN field mapping Bedrock expects, (2) an AOSS data-access policy
# granting this role and the terraform runner aoss:* on the collection, and
# (3) resolving assumed-role session ARNs to their underlying roles. All of
# that is an application-layer concern — the application that ingests data
# into the KB is the right place to create the KB, the vector index, and
# the data-access policy, because only it knows which embedding model /
# dimension / field names it needs. This module provisions only the IAM
# role and its policy so the application can assume it when it calls
# CreateKnowledgeBase and StartIngestionJob.

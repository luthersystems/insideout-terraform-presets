# aws/kendra — managed enterprise search / RAG retrieval (#760, part of #755)
#
# What this builds:
#   • aws_kendra_index        — the Kendra search index (GEN-AI-era RAG
#       retrieval). Always created. Edition is DEVELOPER_EDITION (cost-friendly,
#       single-node) or ENTERPRISE_EDITION (HA, production) — immutable, so the
#       preset's variable validation rejects an out-of-set value at plan time.
#   • aws_iam_role (index)    — the role Kendra assumes to write CloudWatch logs
#       and metrics for the index. Required by aws_kendra_index.role_arn, so it
#       is always created and keeps the preset producing infrastructure even in
#       its minimal (index-only) shape.
#   • aws_kendra_data_source  — an S3 data source connector + its own access
#       role granting s3:GetObject on the wired bucket. Created only when an S3
#       bucket name is wired in (count-gated). In a composed stack DefaultWiring
#       supplies it from module.aws_s3.bucket_name / .bucket_arn when aws_s3 is
#       also selected; a bare Kendra index (no S3) is fully valid (mirrors the
#       aws/bedrock reasoning: the index alone is useful; the S3 source is
#       additive, NOT a hard dependency).
#
# Provisioning note: a Kendra index takes ~30 minutes to provision
# (DEVELOPER_EDITION) and longer for ENTERPRISE_EDITION, so the index carries a
# generous create/update/delete timeout.

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
  subcomponent   = "kendra"
  resource       = "kendra"
}

# Used by the IAM role trust policies below to scope service-principal trust to
# this account+region — AWS's documented mitigation against the cross-account
# confused-deputy attack on kendra.amazonaws.com.
data "aws_caller_identity" "current" {}
data "aws_region" "current" {}
data "aws_partition" "current" {}

locals {
  index_name = var.index_name == null ? "${var.project}-index" : var.index_name

  # The S3 data source (and its access role/policy) only exist when a backing
  # S3 bucket is wired in. A Kendra index with documents ingested out-of-band,
  # or one stood up before its corpus, leaves the data source off.
  has_s3_source = var.s3_bucket_name != null
}

# --- Index IAM role -----------------------------------------------------------
#
# Kendra assumes this role to write the index's CloudWatch logs and metrics. The
# index requires role_arn, so this is always created and keeps the preset
# producing infrastructure even in its minimal (index-only) shape. Trust is
# scoped to the kendra service principal, and the confused-deputy holes are
# closed by pinning aws:SourceAccount to this account and aws:SourceArn to this
# account+region's index ARN namespace (mirrors aws/bedrock_agent's role).
resource "aws_iam_role" "index" {
  name = "${var.project}-kendra-index-role"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Action = "sts:AssumeRole"
        Effect = "Allow"
        Principal = {
          Service = "kendra.amazonaws.com"
        }
        Condition = {
          StringEquals = {
            "aws:SourceAccount" = data.aws_caller_identity.current.account_id
          }
          ArnLike = {
            # Derive the partition from data.aws_partition so the SourceArn
            # condition matches in GovCloud (aws-us-gov) / China (aws-cn), not
            # just commercial aws.
            "aws:SourceArn" = "arn:${data.aws_partition.current.partition}:kendra:${data.aws_region.current.region}:${data.aws_caller_identity.current.account_id}:index/*"
          }
        }
      }
    ]
  })

  tags = merge(module.name.tags, var.tags)
}

# The index role's CloudWatch logs + metrics policy. These are the minimum
# grants Kendra documents for an index role: PutMetricData scoped to the
# AWS/Kendra namespace, plus log-group/stream management under /aws/kendra/*.
# Scoped to /aws/kendra/* so the index can manage ONLY its own log groups, not
# arbitrary ones.
resource "aws_iam_role_policy" "index_cloudwatch" {
  name = "${var.project}-kendra-index-cloudwatch"
  role = aws_iam_role.index.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid      = "PutKendraMetrics"
        Effect   = "Allow"
        Action   = ["cloudwatch:PutMetricData"]
        Resource = "*"
        Condition = {
          StringEquals = {
            "cloudwatch:namespace" = "AWS/Kendra"
          }
        }
      },
      {
        Sid      = "DescribeLogGroups"
        Effect   = "Allow"
        Action   = ["logs:DescribeLogGroups"]
        Resource = "*"
      },
      {
        Sid      = "CreateKendraLogGroup"
        Effect   = "Allow"
        Action   = ["logs:CreateLogGroup"]
        Resource = "arn:${data.aws_partition.current.partition}:logs:${data.aws_region.current.region}:${data.aws_caller_identity.current.account_id}:log-group:/aws/kendra/*"
      },
      {
        Sid    = "WriteKendraLogStreams"
        Effect = "Allow"
        Action = [
          "logs:DescribeLogStreams",
          "logs:CreateLogStream",
          "logs:PutLogEvents",
        ]
        Resource = "arn:${data.aws_partition.current.partition}:logs:${data.aws_region.current.region}:${data.aws_caller_identity.current.account_id}:log-group:/aws/kendra/*:log-stream:*"
      },
    ]
  })
}

# IAM is eventually consistent: the index role/policy created above can take a
# few seconds to propagate before Kendra's CreateIndex will accept it, otherwise
# it can fail validation on the first apply. A short sleep between the policy and
# the index turns a flaky first apply into a reliable one (mirrors
# aws/bedrock's time_sleep.kb_iam_propagation — same IAM-propagation race on a
# control-plane create).
resource "time_sleep" "index_iam_propagation" {
  create_duration = var.iam_propagation_delay
  depends_on      = [aws_iam_role_policy.index_cloudwatch]
}

# --- Index --------------------------------------------------------------------
#
# The Kendra search index. role_arn is the CloudWatch-logging role above.
# server_side_encryption_configuration pins a customer-managed KMS key when one
# is wired in (otherwise Kendra uses an AWS-owned key). The index is the
# unconditional resource that makes this preset always emit infrastructure.
resource "aws_kendra_index" "this" {
  name        = local.index_name
  role_arn    = aws_iam_role.index.arn
  edition     = var.edition
  description = "Insideout Kendra enterprise-search index for ${var.project}."

  user_context_policy = var.user_context_policy

  dynamic "server_side_encryption_configuration" {
    for_each = var.kms_key_id == null ? [] : [1]
    content {
      kms_key_id = var.kms_key_id
    }
  }

  tags = merge(module.name.tags, var.tags)

  # Wait for the index role/policy to propagate before Kendra validates the
  # role on CreateIndex.
  depends_on = [time_sleep.index_iam_propagation]

  timeouts {
    create = "60m"
    update = "60m"
    delete = "60m"
  }
}

# --- S3 data source access role ----------------------------------------------
#
# The role Kendra assumes to crawl the wired S3 bucket. Only created alongside
# the S3 data source. Trust is scoped to the kendra service principal with the
# same confused-deputy guards as the index role, but the SourceArn namespace is
# the data-source ARN namespace of this index.
resource "aws_iam_role" "data_source" {
  count = local.has_s3_source ? 1 : 0

  name = "${var.project}-kendra-s3-source-role"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Action = "sts:AssumeRole"
        Effect = "Allow"
        Principal = {
          Service = "kendra.amazonaws.com"
        }
        Condition = {
          StringEquals = {
            "aws:SourceAccount" = data.aws_caller_identity.current.account_id
          }
          ArnLike = {
            "aws:SourceArn" = "arn:${data.aws_partition.current.partition}:kendra:${data.aws_region.current.region}:${data.aws_caller_identity.current.account_id}:index/${aws_kendra_index.this.id}/data-source/*"
          }
        }
      }
    ]
  })

  tags = merge(module.name.tags, var.tags)
}

# The S3 data-source access policy. s3:GetObject + s3:ListBucket on the wired
# bucket is the only grant, plus the BatchPutDocument/BatchDeleteDocument the
# connector uses to push crawled documents into THIS index. Scoped to the wired
# bucket ARN and this index's ARN — least privilege.
resource "aws_iam_role_policy" "data_source" {
  count = local.has_s3_source ? 1 : 0

  name = "${var.project}-kendra-s3-source"
  role = aws_iam_role.data_source[0].id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid      = "ReadSourceObjects"
        Effect   = "Allow"
        Action   = ["s3:GetObject"]
        Resource = "${local.s3_bucket_arn}/*"
      },
      {
        Sid      = "ListSourceBucket"
        Effect   = "Allow"
        Action   = ["s3:ListBucket"]
        Resource = local.s3_bucket_arn
      },
      {
        Sid    = "IngestToIndex"
        Effect = "Allow"
        Action = [
          "kendra:BatchPutDocument",
          "kendra:BatchDeleteDocument",
        ]
        Resource = aws_kendra_index.this.arn
      },
    ]
  })
}

locals {
  # Prefer an explicitly-wired bucket ARN; otherwise derive it from the bucket
  # name. In a composed stack DefaultWiring supplies both bucket_name and
  # bucket_arn from the aws_s3 module, but a single-module caller may pass only
  # the name — derive a partition-correct ARN so the access policy is still
  # least-privilege rather than wildcarded.
  # Guard the name interpolation: var.s3_bucket_name is null in the bare-index
  # case, and Terraform evaluates this local eagerly at plan time even though
  # only count-gated (has_s3_source) resources consume it — interpolating a null
  # into the template would raise "Invalid template interpolation value". Resolve
  # to null when neither an explicit ARN nor a bucket name is present.
  s3_bucket_arn = var.s3_bucket_arn != null ? var.s3_bucket_arn : (
    var.s3_bucket_name != null ? "arn:${data.aws_partition.current.partition}:s3:::${var.s3_bucket_name}" : null
  )
}

# IAM propagation for the data-source role before the connector validates it.
resource "time_sleep" "data_source_iam_propagation" {
  count = local.has_s3_source ? 1 : 0

  create_duration = var.iam_propagation_delay
  depends_on      = [aws_iam_role_policy.data_source]
}

# --- S3 data source -----------------------------------------------------------
#
# An S3 connector that crawls var.s3_bucket_name into the index. Created only
# when an S3 bucket is wired in. type = "S3" requires role_arn (the access role
# above) and an s3_configuration block naming the bucket.
resource "aws_kendra_data_source" "s3" {
  count = local.has_s3_source ? 1 : 0

  index_id    = aws_kendra_index.this.id
  name        = "${var.project}-s3-source"
  type        = "S3"
  role_arn    = aws_iam_role.data_source[0].arn
  description = "S3 document source for ${local.index_name}."
  schedule    = var.s3_crawl_schedule

  configuration {
    s3_configuration {
      bucket_name = var.s3_bucket_name
    }
  }

  tags = merge(module.name.tags, var.tags)

  # Wait for the access role/policy to propagate before the connector validates
  # the role on CreateDataSource.
  depends_on = [time_sleep.data_source_iam_propagation]
}

# Seed resources for the Stage 2c4 LocalStack discover gate (#272).
#
# One of each of the 8 supported AWS resource types covered by the gate.
# Naming + tagging follows what the awsdiscover/* per-type filters expect:
#   - IAM role/policy, S3:       name prefix == project
#   - DynamoDB:                  name prefix + Project tag
#   - Secrets Manager, Lambda:   Project tag
#   - KMS:                       aws_kms_alias name contains project
#   - CloudWatch Logs:           name contains project (substring)
#
# All taggable resources also carry Project=<project> for symmetry with
# how downstream discovery expects to attribute them (issue #81 / #255).
#
# **SQS is intentionally excluded from this seed.** LocalStack 4.x emits
# queue URLs of the form `http://sqs.<region>.localhost.localstack.cloud:4566/...`
# regardless of `SQS_ENDPOINT_STRATEGY`, and the AWS provider's SQS import
# parser only accepts `sqs.<region>.amazonaws.com` / `<region>.queue.amazonaws.com`
# hostnames. The discover code is correct against real AWS — this is purely
# a LocalStack hostname-shape gap. Tracked as a follow-up so SQS coverage
# rejoins the gate once LocalStack offers an AWS-shaped SQS endpoint or
# we add a queue-name-keyed import path.
#
# This stack is **never composed by InsideOut** — it's a CI fixture
# applied directly against LocalStack (http://localhost:4566) by
# tests/localstack-discover-gate.sh. The lint scripts that target
# InsideOut presets do not scan tests/testdata/.

locals {
  project = "localstack-seed-272"
  tags = {
    Project = local.project
  }
}

# ---------------------------------------------------------------------------
# IAM — execution role + a separate standalone policy.
# ---------------------------------------------------------------------------

resource "aws_iam_role" "lambda_exec" {
  name = "${local.project}-lambda-exec"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Action    = "sts:AssumeRole"
      Effect    = "Allow"
      Principal = { Service = "lambda.amazonaws.com" }
    }]
  })
  tags = local.tags
}

resource "aws_iam_policy" "read_only" {
  name = "${local.project}-read-only"
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Action   = ["s3:GetObject"]
      Effect   = "Allow"
      Resource = "*"
    }]
  })
  tags = local.tags
}

# ---------------------------------------------------------------------------
# KMS — a CMK plus an alias whose name contains the project, so the
# alias-driven kmsDiscoverer pulls the key in.
# ---------------------------------------------------------------------------

resource "aws_kms_key" "main" {
  description             = "${local.project} CMK"
  deletion_window_in_days = 7
  tags                    = local.tags
}

resource "aws_kms_alias" "main" {
  name          = "alias/${local.project}-cmk"
  target_key_id = aws_kms_key.main.key_id
}

# ---------------------------------------------------------------------------
# S3, SQS, DynamoDB, Secrets Manager, CloudWatch Logs.
# ---------------------------------------------------------------------------

resource "aws_s3_bucket" "main" {
  bucket = "${local.project}-bucket"
  tags   = local.tags
}

# aws_sqs_queue intentionally omitted — see file-header note.

resource "aws_dynamodb_table" "main" {
  name         = "${local.project}-table"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "id"

  attribute {
    name = "id"
    type = "S"
  }

  tags = local.tags
}

resource "aws_secretsmanager_secret" "main" {
  name = "${local.project}-secret"
  # Immediate destroy — the gate's terraform destroy must not leave the
  # secret in scheduled-for-deletion state, otherwise the very next
  # apply blows up on `secret with this name is already scheduled for
  # deletion`. Real AWS honors this, LocalStack honors this; the gate
  # is the only environment where back-to-back create/destroy cycles
  # are routine.
  recovery_window_in_days = 0
  tags                    = local.tags
}

resource "aws_cloudwatch_log_group" "main" {
  name              = "/${local.project}/logs"
  retention_in_days = 7
  tags              = local.tags
}

# ---------------------------------------------------------------------------
# Lambda — needs a deployment package on disk. Build one inline via
# archive_file so the seed has zero external file dependencies.
# ---------------------------------------------------------------------------

data "archive_file" "lambda_stub" {
  type        = "zip"
  output_path = "${path.module}/.tmp/lambda_stub.zip"

  source {
    filename = "index.py"
    content  = "def handler(event, context):\n    return {'statusCode': 200}\n"
  }
}

resource "aws_lambda_function" "main" {
  function_name    = "${local.project}-fn"
  role             = aws_iam_role.lambda_exec.arn
  filename         = data.archive_file.lambda_stub.output_path
  source_code_hash = data.archive_file.lambda_stub.output_base64sha256
  handler          = "index.handler"
  runtime          = "python3.11"
  tags             = local.tags
}

# roundtrip-fixture is a tiny, self-contained Terraform stack that creates
# the four AWS resource types implicated in issue #652 — an S3 bucket, a
# CloudWatch log group, a customer-managed IAM policy, and a Lambda
# function (plus the IAM role the Lambda needs). The live round-trip test
# (roundtrip_live_test.go) applies this stack, runs `insideout-import
# discover` against it, re-emits imported.tf via the composer, and
# asserts `terraform plan` on that imported.tf is clean.
#
# Every resource carries `Project = var.project` so the discover
# project-tag filter scopes to exactly this fixture and nothing else in
# the account. var.project is a unique random prefix supplied per run.

terraform {
  required_version = ">= 1.5"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 6.0"
    }
    archive = {
      source  = "hashicorp/archive"
      version = ">= 2.4"
    }
  }
}

variable "project" {
  type        = string
  description = "Unique stack-naming / Project-tag prefix for this run."
}

variable "region" {
  type        = string
  default     = "us-east-1"
  description = "AWS region to create the fixture resources in."
}

provider "aws" {
  region = var.region
}

locals {
  tags = {
    Project = var.project
  }
}

resource "aws_s3_bucket" "fixture" {
  bucket        = "${var.project}-bucket"
  force_destroy = true
  tags          = local.tags
}

resource "aws_cloudwatch_log_group" "fixture" {
  name              = "/insideout-roundtrip/${var.project}"
  retention_in_days = 14
  tags              = local.tags
}

resource "aws_iam_policy" "fixture" {
  name = "${var.project}-policy"
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["s3:GetObject"]
      Resource = "${aws_s3_bucket.fixture.arn}/*"
    }]
  })
  tags = local.tags
}

resource "aws_iam_role" "fixture" {
  name = "${var.project}-lambda-role"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "lambda.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
  tags = local.tags
}

data "archive_file" "lambda" {
  type        = "zip"
  output_path = "${path.module}/lambda.zip"
  source {
    content  = "exports.handler = async () => ({ statusCode: 200 });"
    filename = "index.js"
  }
}

resource "aws_lambda_function" "fixture" {
  function_name    = "${var.project}-fn"
  role             = aws_iam_role.fixture.arn
  runtime          = "nodejs20.x"
  handler          = "index.handler"
  filename         = data.archive_file.lambda.output_path
  source_code_hash = data.archive_file.lambda.output_base64sha256
  tags             = local.tags
}

output "bucket_name" {
  value = aws_s3_bucket.fixture.id
}

output "log_group_name" {
  value = aws_cloudwatch_log_group.fixture.name
}

output "iam_policy_arn" {
  value = aws_iam_policy.fixture.arn
}

output "lambda_function_name" {
  value = aws_lambda_function.fixture.function_name
}

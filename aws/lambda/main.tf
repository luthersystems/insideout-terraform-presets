terraform {
  required_version = ">= 1.5"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 6.0"
    }
    random = {
      source  = "hashicorp/random"
      version = ">= 3.5"
    }
  }
}

# Unique suffix to avoid log group name collisions on destroy/recreate
resource "random_id" "suffix" {
  byte_length = 2
}

module "name" {
  source         = "github.com/luthersystems/tf-modules.git//luthername?ref=v55.13.4"
  luther_project = var.project
  aws_region     = var.region
  luther_env     = var.environment
  org_name       = "luthersystems"
  component      = "insideout"
  subcomponent   = "lambda"
  resource       = "lambda"
  id             = random_id.suffix.hex
}

# -----------------------------------------------------------------------------
# IAM Role for Lambda
# -----------------------------------------------------------------------------
resource "aws_iam_role" "lambda_exec" {
  name = "${var.project}-lambda-exec"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Action = "sts:AssumeRole"
      Effect = "Allow"
      Sid    = ""
      Principal = {
        Service = "lambda.amazonaws.com"
      }
    }]
  })

  tags = merge(module.name.tags, var.tags)
}

resource "aws_iam_role_policy_attachment" "lambda_basic" {
  role       = aws_iam_role.lambda_exec.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"
}

resource "aws_iam_role_policy_attachment" "lambda_vpc" {
  count      = var.enable_vpc ? 1 : 0
  role       = aws_iam_role.lambda_exec.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AWSLambdaVPCAccessExecutionRole"
}

# -----------------------------------------------------------------------------
# Default Security Group (created when VPC-enabled and no SGs provided)
# -----------------------------------------------------------------------------
locals {
  create_default_sg            = var.enable_vpc && length(var.security_group_ids) == 0
  effective_security_group_ids = local.create_default_sg ? [aws_security_group.lambda[0].id] : var.security_group_ids
}

resource "aws_security_group" "lambda" {
  count       = local.create_default_sg ? 1 : 0
  name        = "${var.project}-lambda-sg"
  description = "Default security group for Lambda VPC access"
  vpc_id      = var.vpc_id

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = merge(module.name.tags, { Name = "${var.project}-lambda-sg" }, var.tags)
}

# -----------------------------------------------------------------------------
# CloudWatch Log Group
# -----------------------------------------------------------------------------
resource "aws_cloudwatch_log_group" "lambda" {
  name              = "/aws/lambda/${module.name.name}"
  retention_in_days = var.log_retention_days
  tags              = merge(module.name.tags, var.tags)
}

# -----------------------------------------------------------------------------
# Lambda Function
# -----------------------------------------------------------------------------
resource "aws_lambda_function" "this" {
  function_name = module.name.name
  role          = aws_iam_role.lambda_exec.arn
  handler       = var.handler
  runtime       = var.runtime
  memory_size   = var.memory_size
  timeout       = var.timeout

  # Using a placeholder deployment package
  filename         = "${path.module}/placeholder.zip"
  source_code_hash = fileexists("${path.module}/placeholder.zip") ? filebase64sha256("${path.module}/placeholder.zip") : null

  dynamic "vpc_config" {
    for_each = var.enable_vpc ? [1] : []
    content {
      subnet_ids         = var.subnet_ids
      security_group_ids = local.effective_security_group_ids
    }
  }

  environment {
    variables = var.environment_variables
  }

  tags = merge(module.name.tags, var.tags)

  depends_on = [
    aws_iam_role_policy_attachment.lambda_basic,
    aws_cloudwatch_log_group.lambda,
  ]
}

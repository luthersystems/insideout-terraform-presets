terraform {
  required_version = ">= 1.5"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 6.0"
    }
  }
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

  tags = var.tags
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
# CloudWatch Log Group
# -----------------------------------------------------------------------------
resource "aws_cloudwatch_log_group" "lambda" {
  name              = "/aws/lambda/${var.project}-function"
  retention_in_days = var.log_retention_days
  tags              = var.tags
}

# -----------------------------------------------------------------------------
# Lambda Function
# -----------------------------------------------------------------------------
resource "aws_lambda_function" "this" {
  function_name = "${var.project}-function"
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
      security_group_ids = var.security_group_ids
    }
  }

  environment {
    variables = var.environment_variables
  }

  tags = var.tags

  depends_on = [
    aws_iam_role_policy_attachment.lambda_basic,
    aws_cloudwatch_log_group.lambda,
  ]
}

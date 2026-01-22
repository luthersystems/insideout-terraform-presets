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


# Unique suffix to ensure log group name uniqueness per environment
resource "random_id" "suffix" {
  byte_length = 2
}

# -----------------------------------------------------------------------------
# CloudWatch Logs — app log group (private, optional KMS)
# -----------------------------------------------------------------------------
resource "aws_cloudwatch_log_group" "app" {
  name              = "/${var.project}/${random_id.suffix.hex}/app"
  retention_in_days = var.retention_in_days
  kms_key_id        = var.kms_key_arn != "" ? var.kms_key_arn : null

  tags = merge({ Name = "${var.project}-logs" }, var.tags)
}

# A default stream (handy for quick testing)
resource "aws_cloudwatch_log_stream" "default" {
  name           = "app"
  log_group_name = aws_cloudwatch_log_group.app.name
}

# -----------------------------------------------------------------------------
# IAM role + minimal policy for writers (EC2 by default)
# Attach this role to instances/tasks that should write to the log group.
# -----------------------------------------------------------------------------
data "aws_iam_policy_document" "assume" {
  statement {
    effect  = "Allow"
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = var.writer_principals
    }
  }
}

resource "aws_iam_role" "writer" {
  name               = "${var.project}-cwlogs-writer-${random_id.suffix.hex}"
  assume_role_policy = data.aws_iam_policy_document.assume.json
  tags               = var.tags
}

data "aws_iam_policy_document" "writer" {
  statement {
    effect = "Allow"
    actions = [
      "logs:CreateLogStream",
      "logs:PutLogEvents",
      "logs:DescribeLogStreams"
    ]
    resources = [
      aws_cloudwatch_log_group.app.arn,
      "${aws_cloudwatch_log_group.app.arn}:*"
    ]
  }
  statement {
    effect    = "Allow"
    actions   = ["logs:CreateLogGroup"]
    resources = ["*"]
  }
}

resource "aws_iam_policy" "writer" {
  name   = "${var.project}-cwlogs-writer-${random_id.suffix.hex}"
  policy = data.aws_iam_policy_document.writer.json
}

resource "aws_iam_role_policy_attachment" "writer" {
  role       = aws_iam_role.writer.name
  policy_arn = aws_iam_policy.writer.arn
}

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

module "name" {
  source         = "github.com/luthersystems/tf-modules.git//luthername?ref=v55.15.0"
  luther_project = var.project
  aws_region     = var.region
  luther_env     = var.environment
  org_name       = "luthersystems"
  component      = "insideout"
  subcomponent   = "alb"
  resource       = "alb"
}

# Security group for the public ALB
resource "aws_security_group" "alb_sg" {
  name        = "${module.name.name}-sg"
  description = "Security group for Application Load Balancer"
  vpc_id      = var.vpc_id

  # HTTP
  ingress {
    description = "Allow HTTP"
    from_port   = 80
    to_port     = 80
    protocol    = "tcp"
    cidr_blocks = var.allow_cidrs
  }

  # HTTPS (always open; listener is conditional)
  ingress {
    description = "Allow HTTPS"
    from_port   = 443
    to_port     = 443
    protocol    = "tcp"
    cidr_blocks = var.allow_cidrs
  }

  # Outbound
  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = merge(module.name.tags, { Name = "${module.name.prefix}-sg" }, var.tags)
}

# -----------------------------------------------------------------------------
# Access-logs S3 bucket (owned by this module)
# -----------------------------------------------------------------------------
# ALB access logs unlock per-path / per-status / target-response-time analysis
# that the CloudWatch metric surface alone cannot provide. AWS delivers access
# logs to S3, so we own a dedicated bucket here (one per ALB) with the
# service-principal PutObject grant recommended for all regions — including
# opt-in / post-August-2022 regions where the historical ELB account-ID
# approach does not work.
resource "random_id" "alb_logs_suffix" {
  byte_length = 3
}

data "aws_caller_identity" "current" {}

resource "aws_s3_bucket" "alb_logs" {
  bucket        = "${var.project}-alb-logs-${random_id.alb_logs_suffix.hex}"
  force_destroy = true
  tags          = merge(module.name.tags, { Name = "${module.name.prefix}-alb-logs" }, var.tags)

  # Bucket configuration is split across sibling resources
  # (aws_s3_bucket_policy, _public_access_block, _lifecycle_configuration).
  # The legacy inline attributes remain Computed and are repopulated by
  # refresh from API state, causing drift-check noise.
  lifecycle {
    ignore_changes = [
      acceleration_status,
      acl,
      cors_rule,
      grant,
      lifecycle_rule,
      logging,
      object_lock_configuration,
      policy,
      replication_configuration,
      request_payer,
      server_side_encryption_configuration,
      versioning,
      website,
    ]
  }
}

resource "aws_s3_bucket_public_access_block" "alb_logs" {
  bucket                  = aws_s3_bucket.alb_logs.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

data "aws_iam_policy_document" "alb_logs" {
  statement {
    sid       = "AllowELBAccessLogDelivery"
    effect    = "Allow"
    actions   = ["s3:PutObject"]
    resources = ["${aws_s3_bucket.alb_logs.arn}/AWSLogs/${data.aws_caller_identity.current.account_id}/*"]
    principals {
      type        = "Service"
      identifiers = ["logdelivery.elasticloadbalancing.amazonaws.com"]
    }
  }
}

resource "aws_s3_bucket_policy" "alb_logs" {
  bucket = aws_s3_bucket.alb_logs.id
  policy = data.aws_iam_policy_document.alb_logs.json
}

resource "aws_s3_bucket_lifecycle_configuration" "alb_logs" {
  bucket = aws_s3_bucket.alb_logs.id
  rule {
    id     = "expire-access-logs"
    status = "Enabled"
    filter {}
    expiration {
      days = var.access_logs_retention_days
    }
  }
}

# Internet-facing ALB
resource "aws_lb" "alb" {
  # ALB names limited to 32 chars — use var.project
  name               = "${var.project}-alb"
  load_balancer_type = "application"
  internal           = false
  security_groups    = [aws_security_group.alb_sg.id]
  subnets            = var.public_subnet_ids

  enable_deletion_protection = var.enable_deletion_protection

  access_logs {
    bucket  = aws_s3_bucket.alb_logs.bucket
    enabled = true
  }

  tags = merge(module.name.tags, var.tags)

  depends_on = [aws_s3_bucket_policy.alb_logs]
}

# Default target group (attach ECS/EKS/instances later)
resource "aws_lb_target_group" "app" {
  # Target group names limited to 32 chars — use var.project
  name        = "${var.project}-tg"
  vpc_id      = var.vpc_id
  port        = var.target_port
  protocol    = var.target_protocol
  target_type = var.target_type

  health_check {
    enabled             = true
    interval            = 30
    healthy_threshold   = 2
    unhealthy_threshold = 3
    timeout             = 5
    path                = var.health_check_path
    protocol            = var.health_check_protocol
    matcher             = "200-399"
  }

  tags = merge(module.name.tags, { Name = "${module.name.prefix}-tg" }, var.tags)
}

# HTTP listener → forward (when no cert)
resource "aws_lb_listener" "http_forward" {
  count             = var.certificate_arn == null ? 1 : 0
  load_balancer_arn = aws_lb.alb.arn
  port              = 80
  protocol          = "HTTP"

  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.app.arn
  }

  tags = merge(module.name.tags, var.tags)
}

# HTTP listener → redirect to HTTPS (when cert provided)
resource "aws_lb_listener" "http_redirect" {
  count             = var.certificate_arn == null ? 0 : 1
  load_balancer_arn = aws_lb.alb.arn
  port              = 80
  protocol          = "HTTP"

  default_action {
    type = "redirect"

    redirect {
      port        = "443"
      protocol    = "HTTPS"
      status_code = "HTTP_301"
    }
  }

  tags = merge(module.name.tags, var.tags)
}

# HTTPS listener (when cert provided)
resource "aws_lb_listener" "https" {
  count             = var.certificate_arn == null ? 0 : 1
  load_balancer_arn = aws_lb.alb.arn
  port              = 443
  protocol          = "HTTPS"
  ssl_policy        = "ELBSecurityPolicy-2016-08"
  certificate_arn   = var.certificate_arn

  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.app.arn
  }

  tags = merge(module.name.tags, var.tags)
}

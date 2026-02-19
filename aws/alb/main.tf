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

# Internet-facing ALB
resource "aws_lb" "alb" {
  # ALB names limited to 32 chars — use var.project
  name               = "${var.project}-alb"
  load_balancer_type = "application"
  internal           = false
  security_groups    = [aws_security_group.alb_sg.id]
  subnets            = var.public_subnet_ids

  enable_deletion_protection = var.enable_deletion_protection

  tags = merge(module.name.tags, var.tags)
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
}

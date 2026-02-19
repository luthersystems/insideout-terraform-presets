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
  source         = "github.com/luthersystems/tf-modules.git//luthername?ref=v55.15.0"
  luther_project = var.project
  aws_region     = var.region
  luther_env     = var.environment
  org_name       = "luthersystems"
  component      = "insideout"
  subcomponent   = "redis"
  resource       = "redis"
  id             = random_id.suffix.hex
}

# -----------------------------------------------------------------------------
# Networking
# -----------------------------------------------------------------------------
resource "aws_security_group" "redis" {
  name        = "${module.name.name}-sg"
  description = "Allow Redis (6379) from allowed CIDRs"
  vpc_id      = var.vpc_id

  ingress {
    description = "Redis"
    from_port   = 6379
    to_port     = 6379
    protocol    = "tcp"
    cidr_blocks = var.allowed_cidr_blocks
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = merge(module.name.tags, { Name = "${module.name.prefix}-sg" }, var.tags)
}

resource "aws_elasticache_subnet_group" "this" {
  name       = "${module.name.name}-subnets"
  subnet_ids = var.cache_subnet_ids
  tags       = merge(module.name.tags, { Name = "${module.name.prefix}-subnets" }, var.tags)
}

# Optional custom parameter group (family redis7)
resource "aws_elasticache_parameter_group" "this" {
  name   = "${module.name.name}-pg"
  family = "redis7"

  parameter {
    name  = "maxmemory-policy"
    value = "allkeys-lru"
  }

  tags = merge(module.name.tags, { Name = "${module.name.prefix}-pg" }, var.tags)
}

# -----------------------------------------------------------------------------
# Optional CloudWatch Logs group for Redis log delivery
# -----------------------------------------------------------------------------
resource "aws_cloudwatch_log_group" "redis" {
  count             = var.enable_cloudwatch_logs ? 1 : 0
  name              = "/aws/elasticache/${module.name.name}"
  retention_in_days = 30
  tags              = merge(module.name.tags, { Name = "${module.name.prefix}-logs" }, var.tags)
}

# -----------------------------------------------------------------------------
# Auth token for TLS/ACL
# -----------------------------------------------------------------------------
resource "random_password" "auth" {
  length           = 40
  lower            = true
  upper            = true
  numeric          = true
  special          = true
  min_special      = 1
  override_special = "!@#%^*-_=+?"
}

# -----------------------------------------------------------------------------
# Redis Replication Group (cluster-mode disabled)
# -----------------------------------------------------------------------------
resource "aws_elasticache_replication_group" "this" {
  # Replication group IDs limited to 40 chars — use var.project
  replication_group_id = "${var.project}-redis"
  description          = "Redis for ${var.project}"

  engine         = "redis"
  engine_version = var.engine_version
  node_type      = var.node_type
  port           = 6379

  # Cluster mode disabled; single primary + optional replicas
  num_node_groups         = 1
  replicas_per_node_group = var.replicas

  automatic_failover_enabled = var.ha
  multi_az_enabled           = var.ha

  at_rest_encryption_enabled = true
  transit_encryption_enabled = true
  auth_token                 = random_password.auth.result

  subnet_group_name    = aws_elasticache_subnet_group.this.name
  security_group_ids   = [aws_security_group.redis.id]
  parameter_group_name = aws_elasticache_parameter_group.this.name

  maintenance_window       = var.maintenance_window
  snapshot_window          = var.snapshot_window
  snapshot_retention_limit = var.snapshot_retention_days

  apply_immediately = var.apply_immediately

  # Optional CloudWatch log delivery (engine & slow logs)
  dynamic "log_delivery_configuration" {
    for_each = var.enable_cloudwatch_logs ? [1] : []
    content {
      destination      = aws_cloudwatch_log_group.redis[0].name
      destination_type = "cloudwatch-logs"
      log_format       = "text"
      log_type         = "engine-log"
    }
  }

  dynamic "log_delivery_configuration" {
    for_each = var.enable_cloudwatch_logs ? [1] : []
    content {
      destination      = aws_cloudwatch_log_group.redis[0].name
      destination_type = "cloudwatch-logs"
      log_format       = "text"
      log_type         = "slow-log"
    }
  }

  tags = merge(module.name.tags, var.tags)
}

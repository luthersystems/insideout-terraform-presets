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

# Unique suffix to avoid log group/config name collisions on destroy/recreate
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
  subcomponent   = "msk"
  resource       = "msk"
  id             = random_id.suffix.hex
}

locals {
  name = coalesce(var.cluster_name, module.name.name)
  tags = merge(module.name.tags, var.tags)
}

# Security group for brokers + clients
resource "aws_security_group" "msk" {
  name        = "${local.name}-sg"
  description = "MSK broker access"
  vpc_id      = var.vpc_id

  # Allow broker <-> broker chatter (and ZK/TLS) within the SG
  ingress {
    description = "Intra-broker"
    protocol    = "-1"
    from_port   = 0
    to_port     = 0
    self        = true
  }

  # TLS client traffic (recommended)
  ingress {
    description = "Kafka TLS (9094)"
    from_port   = 9094
    to_port     = 9094
    protocol    = "tcp"
    cidr_blocks = var.client_cidr_blocks
  }

  # Optional plaintext client traffic (not recommended)
  dynamic "ingress" {
    for_each = var.allow_plaintext ? [1] : []
    content {
      description = "Kafka plaintext (9092)"
      from_port   = 9092
      to_port     = 9092
      protocol    = "tcp"
      cidr_blocks = var.client_cidr_blocks
    }
  }

  egress {
    description = "All egress"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = local.tags
}

# Optional cluster-level config (safe, demo-ish defaults)
resource "aws_msk_configuration" "this" {
  name           = "${local.name}-cfg"
  kafka_versions = [var.kafka_version]

  server_properties = <<-PROPS
    auto.create.topics.enable = true
    delete.topic.enable = true
    log.retention.hours = 168
    default.replication.factor = 3
    min.insync.replicas = 2
  PROPS
}

# CloudWatch Log Group (optional)
resource "aws_cloudwatch_log_group" "msk" {
  count             = var.enable_cloudwatch_logs ? 1 : 0
  name              = "/aws/msk/${local.name}"
  retention_in_days = var.cloudwatch_retention_days
  tags              = local.tags
}

# The MSK cluster (provisioned)
resource "aws_msk_cluster" "this" {
  cluster_name           = local.name
  kafka_version          = var.kafka_version
  number_of_broker_nodes = var.number_of_broker_nodes
  enhanced_monitoring    = var.enhanced_monitoring # DEFAULT | PER_BROKER | PER_TOPIC_PER_BROKER

  broker_node_group_info {
    instance_type   = var.broker_instance_type
    client_subnets  = var.subnet_ids
    security_groups = [aws_security_group.msk.id]

    storage_info {
      ebs_storage_info {
        volume_size = var.broker_ebs_volume_size
      }
    }
  }

  encryption_info {
    encryption_at_rest_kms_key_arn = var.kms_key_arn

    encryption_in_transit {
      in_cluster    = true
      client_broker = var.allow_plaintext ? "PLAINTEXT" : "TLS"
    }
  }

  configuration_info {
    arn      = aws_msk_configuration.this.arn
    revision = aws_msk_configuration.this.latest_revision
  }

  dynamic "logging_info" {
    for_each = var.enable_cloudwatch_logs ? [1] : []
    content {
      broker_logs {
        cloudwatch_logs {
          enabled   = true
          log_group = aws_cloudwatch_log_group.msk[0].name
        }
      }
    }
  }

  tags = local.tags
}

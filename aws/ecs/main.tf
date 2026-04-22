# AWS ECS Cluster Module (Fargate-first, cluster-only)
# Users define their own services and task definitions downstream.

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
  subcomponent   = "ecs"
  resource       = "ecs"
}

locals {
  # Cluster name is an immutable ForceNew attribute on aws_ecs_cluster;
  # preserve the historical format to avoid replacing existing clusters.
  cluster_name = "${var.project}-ecs"
}

resource "aws_ecs_cluster" "this" {
  name = local.cluster_name

  setting {
    name  = "containerInsights"
    value = var.enable_container_insights ? "enabled" : "disabled"
  }

  tags = merge(
    module.name.tags,
    { Name = local.cluster_name },
    var.tags,
  )
}

resource "aws_ecs_cluster_capacity_providers" "this" {
  cluster_name       = aws_ecs_cluster.this.name
  capacity_providers = var.capacity_providers

  default_capacity_provider_strategy {
    capacity_provider = var.default_capacity_provider
    weight            = 1
    base              = 1
  }
}

# Service Connect namespace for service-to-service discovery
resource "aws_service_discovery_private_dns_namespace" "this" {
  count = var.enable_service_connect ? 1 : 0

  name        = "${local.cluster_name}.local"
  description = "Service Connect namespace for ${local.cluster_name}"
  vpc         = var.vpc_id

  tags = merge(
    module.name.tags,
    { Name = "${local.cluster_name}.local" },
    var.tags,
  )
}

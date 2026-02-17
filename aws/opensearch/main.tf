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
  source         = "github.com/luthersystems/tf-modules.git//luthername?ref=v55.13.4"
  luther_project = var.project
  aws_region     = var.region
  luther_env     = var.environment
  org_name       = "luthersystems"
  component      = "insideout"
  subcomponent   = "os"
  resource       = "os"
}

resource "aws_security_group" "opensearch" {
  name        = "${module.name.name}-sg"
  description = "Security group for OpenSearch domain"
  vpc_id      = var.vpc_id

  ingress {
    from_port   = 443
    to_port     = 443
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"] # Should be restricted in production
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = merge(module.name.tags, { Name = "${module.name.name}-sg" }, var.tags)
}

resource "aws_opensearch_domain" "managed" {
  count = var.deployment_type == "managed" ? 1 : 0

  domain_name    = module.name.name
  engine_version = "OpenSearch_2.11"

  cluster_config {
    instance_type          = var.instance_type
    instance_count         = var.multi_az ? 2 : 1
    zone_awareness_enabled = var.multi_az
  }

  ebs_options {
    ebs_enabled = true
    volume_size = tonumber(replace(var.storage_size, "GB", ""))
    volume_type = "gp3"
  }

  vpc_options {
    subnet_ids         = [var.subnet_ids[0]]
    security_group_ids = [aws_security_group.opensearch.id]
  }

  tags = merge(module.name.tags, { Domain = module.name.name }, var.tags)
}

resource "aws_opensearchserverless_collection" "serverless" {
  count = var.deployment_type == "serverless" ? 1 : 0

  name = module.name.name
  type = "VECTORSEARCH"

  tags = merge(module.name.tags, { Name = module.name.name }, var.tags)
}


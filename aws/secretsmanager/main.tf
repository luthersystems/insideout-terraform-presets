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

# Unique suffix to avoid name collisions with deleted secrets (7-day tombstone)
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
  subcomponent   = "sm"
  resource       = "sm"
  id             = random_id.suffix.hex
  replication    = var.num_secrets
}

resource "aws_secretsmanager_secret" "secrets" {
  count = var.num_secrets
  name  = module.name.names[count.index]

  tags = merge(module.name.tags, { Name = "${module.name.prefix}-${count.index}" }, var.tags)
}

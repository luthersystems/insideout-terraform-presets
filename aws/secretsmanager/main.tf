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
  subcomponent   = "secretsmanager"
  resource       = "secretsmanager"
}

resource "aws_secretsmanager_secret" "secrets" {
  count = var.num_secrets
  name  = "${var.project}-secret-${count.index}"

  tags = merge(module.name.tags, { Name = "${module.name.prefix}-${count.index}" }, var.tags)
}

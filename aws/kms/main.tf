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
  subcomponent   = "kms"
  resource       = "kms"
}

resource "aws_kms_key" "keys" {
  count                   = var.num_keys
  description             = "KMS key ${count.index} for ${var.project}"
  deletion_window_in_days = 7
  enable_key_rotation     = true

  tags = merge(module.name.tags, { Name = "${module.name.prefix}-${count.index}" }, var.tags)
}

resource "aws_kms_alias" "aliases" {
  count         = var.num_keys
  name          = "alias/${var.project}-key-${count.index}"
  target_key_id = aws_kms_key.keys[count.index].key_id
}

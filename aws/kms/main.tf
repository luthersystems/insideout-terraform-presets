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

# Unique suffix to avoid alias collisions with pending-deletion keys (7-30 day window)
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
  subcomponent   = "kms"
  resource       = "kms"
  id             = random_id.suffix.hex
  replication    = var.num_keys
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
  name          = "alias/${module.name.names[count.index]}"
  target_key_id = aws_kms_key.keys[count.index].key_id
}

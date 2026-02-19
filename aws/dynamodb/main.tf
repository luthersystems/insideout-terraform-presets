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
  subcomponent   = "dynamodb"
  resource       = "dynamodb"
}

locals {
  table_name = coalesce(var.table_name, "${var.project}-app")
  tags       = merge(module.name.tags, var.tags)
}

resource "aws_dynamodb_table" "this" {
  name         = local.table_name
  billing_mode = var.billing_mode # "PAY_PER_REQUEST" or "PROVISIONED"

  # Keys
  hash_key  = var.hash_key
  range_key = var.range_key != "" ? var.range_key : null

  # Provisioned throughput only when billing_mode == "PROVISIONED"
  read_capacity  = var.billing_mode == "PROVISIONED" ? var.read_capacity : null
  write_capacity = var.billing_mode == "PROVISIONED" ? var.write_capacity : null

  # TTL
  ttl {
    attribute_name = var.ttl_attribute
    enabled        = var.ttl_enabled
  }

  # PITR
  point_in_time_recovery {
    enabled = var.point_in_time_recovery
  }

  # Streams
  stream_enabled   = var.stream_enabled
  stream_view_type = var.stream_enabled ? var.stream_view_type : null

  # Encryption (AWS managed key by default; allow override)
  server_side_encryption {
    enabled     = true
    kms_key_arn = var.kms_key_arn
  }

  # Attributes for PK / SK
  attribute {
    name = var.hash_key
    type = "S"
  }

  dynamic "attribute" {
    for_each = var.range_key != "" ? [var.range_key] : []
    content {
      name = attribute.value
      type = "S"
    }
  }

  # GSIs
  dynamic "global_secondary_index" {
    for_each = var.global_secondary_indexes
    content {
      name            = global_secondary_index.value.name
      hash_key        = global_secondary_index.value.hash_key
      range_key       = try(global_secondary_index.value.range_key, "") != "" ? global_secondary_index.value.range_key : null
      projection_type = global_secondary_index.value.projection_type

      non_key_attributes = try(global_secondary_index.value.non_key_attributes, null)

      # Only set capacities if table is PROVISIONED
      read_capacity  = var.billing_mode == "PROVISIONED" ? try(global_secondary_index.value.read_capacity, var.read_capacity) : null
      write_capacity = var.billing_mode == "PROVISIONED" ? try(global_secondary_index.value.write_capacity, var.write_capacity) : null
    }
  }

  tags = local.tags
}

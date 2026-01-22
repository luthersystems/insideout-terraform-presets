terraform {
  required_version = ">= 1.5"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 6.0"
    }
  }
}


resource "aws_kms_key" "keys" {
  count                   = var.num_keys
  description             = "KMS key ${count.index} for ${var.project}"
  deletion_window_in_days = 7
  enable_key_rotation     = true

  tags = merge({ Name = "${var.project}-key-${count.index}" }, var.tags)
}

resource "aws_kms_alias" "aliases" {
  count         = var.num_keys
  name          = "alias/${var.project}-key-${count.index}"
  target_key_id = aws_kms_key.keys[count.index].key_id
}

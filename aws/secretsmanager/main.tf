terraform {
  required_version = ">= 1.5"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 6.0"
    }
  }
}


resource "aws_secretsmanager_secret" "secrets" {
  count = var.num_secrets
  name  = "${var.project}-secret-${count.index}"

  tags = merge({ Name = "${var.project}-secret-${count.index}" }, var.tags)
}

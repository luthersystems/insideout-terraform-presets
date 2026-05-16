terraform {
  required_version = ">= 1.5"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 6.0"
    }
  }
}

provider "aws" {
  region = var.vpc_region
  default_tags {
    tags = {
      Project    = var.project
      managed-by = "insideout"
    }
  }
}

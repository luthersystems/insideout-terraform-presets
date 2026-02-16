terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 6.0"
    }
  }
}

provider "aws" {
  region = "us-west-2"
  default_tags {
    tags = {
      managed-by = "insideout"
    }
  }
}

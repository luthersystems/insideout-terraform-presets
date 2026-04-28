terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 5.70.0"
    }
    google = {
      source  = "hashicorp/google"
      version = "= 6.10.0"
    }
  }
}

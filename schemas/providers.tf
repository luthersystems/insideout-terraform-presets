terraform {
  required_version = ">= 1.5"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 5.70.0"
    }
    google = {
      source  = "hashicorp/google"
      version = "= 6.10.0"
    }
    google-beta = {
      source  = "hashicorp/google-beta"
      version = "= 6.10.0"
    }
  }
}

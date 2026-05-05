# Seed stack provider — points the AWS provider at LocalStack
# (http://localhost:4566). The attribute set must stay in sync with
# cmd/insideout-import/genconfig/emit.go::emitProviders' LocalStack branch
# so the seed and the discover-generated stack speak the same dialect.
#
# Used only by tests/localstack-discover-gate.sh (Stage 2c4 / #272).

terraform {
  required_version = ">= 1.5"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.0"
    }
    # archive is local-only — used to build the inline Lambda zip in
    # main.tf without committing a binary fixture to the repo.
    archive = {
      source  = "hashicorp/archive"
      version = "~> 2.4"
    }
  }
}

provider "aws" {
  region                      = "us-east-1"
  access_key                  = "test"
  secret_key                  = "test"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true
  s3_use_path_style           = true

  endpoints {
    cloudwatchlogs = "http://localhost:4566"
    dynamodb       = "http://localhost:4566"
    iam            = "http://localhost:4566"
    kms            = "http://localhost:4566"
    lambda         = "http://localhost:4566"
    s3             = "http://localhost:4566"
    secretsmanager = "http://localhost:4566"
    sqs            = "http://localhost:4566"
    sts            = "http://localhost:4566"
  }
}

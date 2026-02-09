module "vpc" {
  source  = "../../aws/vpc"
  project = var.vpc_project
  region  = var.vpc_region
}

module "lambda" {
  source             = "../../aws/lambda"
  vpc_id             = module.vpc.vpc_id
  subnet_ids         = module.vpc.private_subnet_ids
  security_group_ids = []
  project            = var.lambda_project
  region             = var.lambda_region
  runtime            = var.lambda_runtime
}

module "s3" {
  source     = "../../aws/s3"
  project    = var.s3_project
  region     = var.s3_region
  versioning = var.s3_versioning
}

module "waf" {
  source    = "../../aws/waf"
  providers = { aws = aws, aws.us_east_1 = aws.us_east_1 }
  scope     = "CLOUDFRONT"
  region    = "us-east-1"
  project   = var.waf_project
}

module "backups" {
  source          = "../../aws/backups"
  enable_ec2_ebs  = false
  enable_rds      = true
  enable_dynamodb = false
  enable_s3       = true
  ec2_ebs_rule = {
    selection = {
      resource_arns  = []
      selection_tags = [{ type = "STRINGEQUALS", key = "backup", value = "true" }]
    }
  }
  default_rule = var.backups_default_rule
  project      = var.backups_project
  region       = var.backups_region
}

module "cloudwatchlogs" {
  source  = "../../aws/cloudwatchlogs"
  project = var.cloudwatchlogs_project
  region  = var.cloudwatchlogs_region
}

module "cloudwatchmonitoring" {
  source         = "../../aws/cloudwatchmonitoring"
  sqs_queue_arns = [module.sqs.queue_arn]
  project        = var.cloudwatchmonitoring_project
  region         = var.cloudwatchmonitoring_region
}

module "cognito" {
  source       = "../../aws/cognito"
  mfa_required = var.cognito_mfa_required
  project      = var.cognito_project
  region       = var.cognito_region
  sign_in_type = var.cognito_sign_in_type
}

module "apigateway" {
  source  = "../../aws/apigateway"
  project = var.apigateway_project
  region  = var.apigateway_region
}

module "kms" {
  source  = "../../aws/kms"
  project = var.kms_project
  region  = var.kms_region
}

module "secretsmanager" {
  source  = "../../aws/secretsmanager"
  project = var.secretsmanager_project
  region  = var.secretsmanager_region
}

module "sqs" {
  source  = "../../aws/sqs"
  project = var.sqs_project
  region  = var.sqs_region
}

module "githubactions" {
  source  = "../../aws/githubactions"
  project = var.githubactions_project
}

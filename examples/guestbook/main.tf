module "vpc" {
  source  = "../../aws/vpc"
  project = var.vpc_project
  region  = var.vpc_region
}

module "lambda" {
  source             = "../../aws/lambda"
  enable_vpc         = true
  security_group_ids = []
  vpc_id             = module.vpc.vpc_id
  subnet_ids         = module.vpc.private_subnet_ids
  project            = var.lambda_project
  region             = var.lambda_region
  runtime            = var.lambda_runtime
}

module "alb" {
  source            = "../../aws/alb"
  public_subnet_ids = module.vpc.public_subnet_ids
  vpc_id            = module.vpc.vpc_id
  region            = var.alb_region
  project           = var.alb_project
}

module "s3" {
  source     = "../../aws/s3"
  region     = var.s3_region
  versioning = var.s3_versioning
  project    = var.s3_project
}

module "dynamodb" {
  source       = "../../aws/dynamodb"
  billing_mode = var.dynamodb_billing_mode
  project      = var.dynamodb_project
  region       = var.dynamodb_region
}

module "cloudfront" {
  source               = "../../aws/cloudfront"
  origin_type          = "http"
  custom_origin_domain = module.alb.alb_dns_name
  web_acl_id           = module.waf.web_acl_arn
  project              = var.cloudfront_project
  region               = var.cloudfront_region
}

module "waf" {
  source    = "../../aws/waf"
  providers = { aws = aws, aws.us_east_1 = aws.us_east_1 }
  region    = "us-east-1"
  scope     = "CLOUDFRONT"
  project   = var.waf_project
}

module "cloudwatchlogs" {
  source  = "../../aws/cloudwatchlogs"
  region  = var.cloudwatchlogs_region
  project = var.cloudwatchlogs_project
}

module "cloudwatchmonitoring" {
  source           = "../../aws/cloudwatchmonitoring"
  alb_arn_suffixes = [module.alb.alb_arn_suffix]
  project          = var.cloudwatchmonitoring_project
  region           = var.cloudwatchmonitoring_region
}

module "apigateway" {
  source  = "../../aws/apigateway"
  project = var.apigateway_project
  region  = var.apigateway_region
}

module "secretsmanager" {
  source  = "../../aws/secretsmanager"
  project = var.secretsmanager_project
  region  = var.secretsmanager_region
}

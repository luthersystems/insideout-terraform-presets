module "vpc" {
  source  = "../../aws/vpc"
  project = var.vpc_project
  region  = var.vpc_region
}

module "lambda" {
  source             = "../../aws/lambda"
  enable_vpc         = true
  subnet_ids         = module.vpc.private_subnet_ids
  security_group_ids = []
  vpc_id             = module.vpc.vpc_id
  project            = var.lambda_project
  region             = var.lambda_region
  runtime            = var.lambda_runtime
}

module "alb" {
  source            = "../../aws/alb"
  vpc_id            = module.vpc.vpc_id
  public_subnet_ids = module.vpc.public_subnet_ids
  project           = var.alb_project
  region            = var.alb_region
}

module "elasticache" {
  source           = "../../aws/elasticache"
  vpc_id           = module.vpc.vpc_id
  cache_subnet_ids = module.vpc.private_subnet_ids
  ha               = var.elasticache_ha
  project          = var.elasticache_project
  region           = var.elasticache_region
}

module "s3" {
  source     = "../../aws/s3"
  project    = var.s3_project
  region     = var.s3_region
  versioning = var.s3_versioning
}

module "cloudfront" {
  source               = "../../aws/cloudfront"
  origin_type          = "http"
  custom_origin_domain = module.alb.alb_dns_name
  project              = var.cloudfront_project
  region               = var.cloudfront_region
}

module "cloudwatchlogs" {
  source  = "../../aws/cloudwatchlogs"
  project = var.cloudwatchlogs_project
  region  = var.cloudwatchlogs_region
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

module "vpc" {
  source      = "../../aws/vpc"
  project     = var.vpc_project
  environment = var.environment
  region      = var.vpc_region
}

module "lambda" {
  source             = "../../aws/lambda"
  enable_vpc         = true
  vpc_id             = module.vpc.vpc_id
  subnet_ids         = module.vpc.private_subnet_ids
  security_group_ids = []
  region             = var.lambda_region
  runtime            = var.lambda_runtime
  timeout            = var.lambda_timeout
  memory_size        = var.lambda_memory_size
  project            = var.lambda_project
  environment        = var.environment
}

module "alb" {
  source            = "../../aws/alb"
  vpc_id            = module.vpc.vpc_id
  public_subnet_ids = module.vpc.public_subnet_ids
  project           = var.alb_project
  environment       = var.environment
  region            = var.alb_region
}

module "s3" {
  source      = "../../aws/s3"
  project     = var.s3_project
  environment = var.environment
  region      = var.s3_region
  versioning  = var.s3_versioning
}

module "cloudfront" {
  source               = "../../aws/cloudfront"
  custom_origin_domain = module.alb.alb_dns_name
  origin_type          = "http"
  project              = var.cloudfront_project
  environment          = var.environment
  region               = var.cloudfront_region
}

module "backups" {
  source          = "../../aws/backups"
  enable_ec2_ebs  = false
  enable_rds      = true
  enable_dynamodb = false
  enable_s3       = false
  ec2_ebs_rule = {
    selection = {
      resource_arns  = []
      selection_tags = [{ type = "STRINGEQUALS", key = "backup", value = "true" }]
    }
  }
  default_rule = var.backups_default_rule
  project      = var.backups_project
  environment  = var.environment
  region       = var.backups_region
}

module "cloudwatchlogs" {
  source      = "../../aws/cloudwatchlogs"
  project     = var.cloudwatchlogs_project
  environment = var.environment
  region      = var.cloudwatchlogs_region
}

module "cognito" {
  source       = "../../aws/cognito"
  region       = var.cognito_region
  sign_in_type = var.cognito_sign_in_type
  project      = var.cognito_project
  environment  = var.environment
}

module "apigateway" {
  source      = "../../aws/apigateway"
  project     = var.apigateway_project
  environment = var.environment
  region      = var.apigateway_region
}

module "secretsmanager" {
  source      = "../../aws/secretsmanager"
  num_secrets = var.secretsmanager_num_secrets
  project     = var.secretsmanager_project
  environment = var.environment
  region      = var.secretsmanager_region
}

module "githubactions" {
  source      = "../../aws/githubactions"
  project     = var.githubactions_project
  environment = var.environment
}

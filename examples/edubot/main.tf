module "vpc" {
  source      = "../../aws/vpc"
  project     = var.vpc_project
  region      = var.vpc_region
  environment = var.environment
}

module "lambda" {
  source      = "../../aws/lambda"
  enable_vpc  = true
  vpc_id      = module.vpc.vpc_id
  subnet_ids  = module.vpc.private_subnet_ids
  project     = var.lambda_project
  region      = var.lambda_region
  environment = var.environment
  runtime     = var.lambda_runtime
}

module "alb" {
  source            = "../../aws/alb"
  vpc_id            = module.vpc.vpc_id
  public_subnet_ids = module.vpc.public_subnet_ids
  project           = var.alb_project
  region            = var.alb_region
  environment       = var.environment
}

module "s3" {
  source      = "../../aws/s3"
  project     = var.s3_project
  region      = var.s3_region
  environment = var.environment
  versioning  = var.s3_versioning
}

module "dynamodb" {
  source       = "../../aws/dynamodb"
  billing_mode = var.dynamodb_billing_mode
  project      = var.dynamodb_project
  region       = var.dynamodb_region
  environment  = var.environment
}

module "cloudfront" {
  source               = "../../aws/cloudfront"
  web_acl_id           = module.waf.web_acl_arn
  origin_type          = "http"
  custom_origin_domain = module.alb.alb_dns_name
  project              = var.cloudfront_project
  region               = var.cloudfront_region
  environment          = var.environment
}

module "waf" {
  source      = "../../aws/waf"
  providers   = { aws = aws, aws.us_east_1 = aws.us_east_1 }
  scope       = "CLOUDFRONT"
  region      = "us-east-1"
  project     = var.waf_project
  environment = var.environment
}

module "cloudwatchlogs" {
  source      = "../../aws/cloudwatchlogs"
  project     = var.cloudwatchlogs_project
  region      = var.cloudwatchlogs_region
  environment = var.environment
}

module "cloudwatchmonitoring" {
  source           = "../../aws/cloudwatchmonitoring"
  alb_arn_suffixes = [module.alb.alb_arn_suffix]
  project          = var.cloudwatchmonitoring_project
  region           = var.cloudwatchmonitoring_region
  environment      = var.environment
}

module "cognito" {
  source       = "../../aws/cognito"
  project      = var.cognito_project
  region       = var.cognito_region
  environment  = var.environment
  sign_in_type = var.cognito_sign_in_type
}

module "apigateway" {
  source      = "../../aws/apigateway"
  project     = var.apigateway_project
  region      = var.apigateway_region
  environment = var.environment
}

module "kms" {
  source      = "../../aws/kms"
  project     = var.kms_project
  region      = var.kms_region
  environment = var.environment
}

module "secretsmanager" {
  source      = "../../aws/secretsmanager"
  project     = var.secretsmanager_project
  region      = var.secretsmanager_region
  environment = var.environment
}

module "opensearch" {
  source      = "../../aws/opensearch"
  vpc_id      = module.vpc.vpc_id
  subnet_ids  = module.vpc.private_subnet_ids
  project     = var.opensearch_project
  region      = var.opensearch_region
  environment = var.environment
}

module "bedrock" {
  source         = "../../aws/bedrock"
  s3_bucket_arn  = module.s3.bucket_arn
  opensearch_arn = module.opensearch.opensearch_arn
  project        = var.bedrock_project
  region         = var.bedrock_region
  environment    = var.environment
}

module "githubactions" {
  source  = "../../aws/githubactions"
  project = var.githubactions_project
}

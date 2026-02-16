module "vpc" {
  source      = "../../aws/vpc"
  project     = var.vpc_project
  environment = var.environment
  region      = var.vpc_region
}

module "resource" {
  source      = "../../aws/lambda"
  enable_vpc  = true
  subnet_ids  = module.vpc.private_subnet_ids
  vpc_id      = module.vpc.vpc_id
  region      = var.resource_region
  runtime     = var.resource_runtime
  timeout     = var.resource_timeout
  memory_size = var.resource_memory_size
  project     = var.resource_project
  environment = var.environment
}

module "lambda" {
  source      = "../../aws/lambda"
  enable_vpc  = true
  vpc_id      = module.vpc.vpc_id
  subnet_ids  = module.vpc.private_subnet_ids
  memory_size = var.lambda_memory_size
  project     = var.lambda_project
  environment = var.environment
  region      = var.lambda_region
  runtime     = var.lambda_runtime
  timeout     = var.lambda_timeout
}

module "ec2" {
  source         = "../../aws/eks_nodegroup"
  subnet_ids     = module.vpc.private_subnet_ids
  desired_size   = var.ec2_desired_size
  instance_types = var.ec2_instance_types
  max_size       = var.ec2_max_size
  min_size       = var.ec2_min_size
  project        = var.ec2_project
  environment    = var.environment
  region         = var.ec2_region
  cluster_name   = var.ec2_cluster_name
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

module "dynamodb" {
  source       = "../../aws/dynamodb"
  billing_mode = var.dynamodb_billing_mode
  project      = var.dynamodb_project
  environment  = var.environment
  region       = var.dynamodb_region
}

module "cloudfront" {
  source               = "../../aws/cloudfront"
  origin_type          = "http"
  custom_origin_domain = module.alb.alb_dns_name
  web_acl_id           = module.waf.web_acl_arn
  project              = var.cloudfront_project
  environment          = var.environment
  region               = var.cloudfront_region
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

module "secretsmanager" {
  source      = "../../aws/secretsmanager"
  project     = var.secretsmanager_project
  environment = var.environment
  region      = var.secretsmanager_region
}

module "githubactions" {
  source  = "../../aws/githubactions"
  project = var.githubactions_project
}

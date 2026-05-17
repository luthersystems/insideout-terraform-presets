module "vpc" {
  source      = "../../aws/vpc"
  project     = var.vpc_project
  environment = var.environment
  region      = var.vpc_region
}

module "aws_lambda" {
  source      = "../../aws/lambda"
  enable_vpc  = true
  vpc_id      = module.vpc.vpc_id
  subnet_ids  = module.vpc.private_subnet_ids
  memory_size = var.aws_lambda_memory_size
  project     = var.aws_lambda_project
  environment = var.environment
  region      = var.aws_lambda_region
  runtime     = var.aws_lambda_runtime
  timeout     = var.aws_lambda_timeout
}

module "aws_eks_nodegroup" {
  source         = "../../aws/eks_nodegroup"
  subnet_ids     = module.vpc.private_subnet_ids
  desired_size   = var.aws_eks_nodegroup_desired_size
  instance_types = var.aws_eks_nodegroup_instance_types
  max_size       = var.aws_eks_nodegroup_max_size
  min_size       = var.aws_eks_nodegroup_min_size
  project        = var.aws_eks_nodegroup_project
  environment    = var.environment
  region         = var.aws_eks_nodegroup_region
  cluster_name   = var.aws_eks_nodegroup_cluster_name
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

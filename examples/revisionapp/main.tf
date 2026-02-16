module "vpc" {
  source      = "../../aws/vpc"
  project     = var.vpc_project
  environment = var.environment
  region      = var.vpc_region
}

module "resource" {
  source                    = "../../aws/resource"
  private_subnet_ids        = module.vpc.private_subnet_ids
  public_subnet_ids         = module.vpc.public_subnet_ids
  cluster_enabled_log_types = ["api", "audit", "authenticator", "controllerManager", "scheduler"]
  vpc_id                    = module.vpc.vpc_id
  project                   = var.resource_project
  environment               = var.environment
  region                    = var.resource_region
}

module "lambda" {
  source             = "../../aws/lambda"
  enable_vpc         = true
  vpc_id             = module.vpc.vpc_id
  subnet_ids         = module.vpc.private_subnet_ids
  security_group_ids = []
  project            = var.lambda_project
  environment        = var.environment
  region             = var.lambda_region
  runtime            = var.lambda_runtime
}

module "ec2" {
  source         = "../../aws/eks_nodegroup"
  cluster_name   = module.resource.cluster_name
  subnet_ids     = module.vpc.private_subnet_ids
  desired_size   = var.ec2_desired_size
  instance_types = var.ec2_instance_types
  max_size       = var.ec2_max_size
  min_size       = var.ec2_min_size
  project        = var.ec2_project
  environment    = var.environment
  region         = var.ec2_region
}

module "alb" {
  source            = "../../aws/alb"
  vpc_id            = module.vpc.vpc_id
  public_subnet_ids = module.vpc.public_subnet_ids
  project           = var.alb_project
  environment       = var.environment
  region            = var.alb_region
}

module "elasticache" {
  source           = "../../aws/elasticache"
  vpc_id           = module.vpc.vpc_id
  cache_subnet_ids = module.vpc.private_subnet_ids
  ha               = var.elasticache_ha
  project          = var.elasticache_project
  environment      = var.environment
  region           = var.elasticache_region
  replicas         = var.elasticache_replicas
}

module "s3" {
  source      = "../../aws/s3"
  project     = var.s3_project
  environment = var.environment
  region      = var.s3_region
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

module "cloudwatchmonitoring" {
  source           = "../../aws/cloudwatchmonitoring"
  alb_arn_suffixes = [module.alb.alb_arn_suffix]
  sqs_queue_arns   = [module.sqs.queue_arn]
  project          = var.cloudwatchmonitoring_project
  environment      = var.environment
  region           = var.cloudwatchmonitoring_region
}

module "cognito" {
  source       = "../../aws/cognito"
  mfa_required = var.cognito_mfa_required
  project      = var.cognito_project
  environment  = var.environment
  region       = var.cognito_region
  sign_in_type = var.cognito_sign_in_type
}

module "apigateway" {
  source      = "../../aws/apigateway"
  project     = var.apigateway_project
  environment = var.environment
  region      = var.apigateway_region
}

module "kms" {
  source      = "../../aws/kms"
  project     = var.kms_project
  environment = var.environment
  region      = var.kms_region
}

module "secretsmanager" {
  source      = "../../aws/secretsmanager"
  project     = var.secretsmanager_project
  environment = var.environment
  region      = var.secretsmanager_region
}

module "opensearch" {
  source      = "../../aws/opensearch"
  vpc_id      = module.vpc.vpc_id
  subnet_ids  = module.vpc.private_subnet_ids
  project     = var.opensearch_project
  environment = var.environment
  region      = var.opensearch_region
}

module "bedrock" {
  source         = "../../aws/bedrock"
  s3_bucket_arn  = module.s3.bucket_arn
  opensearch_arn = module.opensearch.opensearch_arn
  project        = var.bedrock_project
  environment    = var.environment
  region         = var.bedrock_region
}

module "sqs" {
  source      = "../../aws/sqs"
  project     = var.sqs_project
  environment = var.environment
  region      = var.sqs_region
}

module "githubactions" {
  source      = "../../aws/githubactions"
  project     = var.githubactions_project
  environment = var.environment
}

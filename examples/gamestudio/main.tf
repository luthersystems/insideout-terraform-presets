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
  memory_size        = var.lambda_memory_size
  project            = var.lambda_project
  environment        = var.environment
  region             = var.lambda_region
  runtime            = var.lambda_runtime
  timeout            = var.lambda_timeout
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
  region      = var.sqs_region
  project     = var.sqs_project
  environment = var.environment
}

module "githubactions" {
  source  = "../../aws/githubactions"
  project = var.githubactions_project
}

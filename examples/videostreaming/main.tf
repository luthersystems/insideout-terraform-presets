module "vpc" {
  source  = "../../aws/vpc"
  project = var.vpc_project
  region  = var.vpc_region
}

module "resource" {
  source                    = "../../aws/resource"
  vpc_id                    = module.vpc.vpc_id
  private_subnet_ids        = module.vpc.private_subnet_ids
  public_subnet_ids         = module.vpc.public_subnet_ids
  cluster_enabled_log_types = ["api", "audit", "authenticator", "controllerManager", "scheduler"]
  region                    = var.resource_region
  project                   = var.resource_project
}

module "alb" {
  source            = "../../aws/alb"
  vpc_id            = module.vpc.vpc_id
  public_subnet_ids = module.vpc.public_subnet_ids
  project           = var.alb_project
  region            = var.alb_region
}

module "s3" {
  source     = "../../aws/s3"
  project    = var.s3_project
  region     = var.s3_region
  versioning = var.s3_versioning
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
  project              = var.cloudfront_project
  region               = var.cloudfront_region
}

module "backups" {
  source          = "../../aws/backups"
  enable_rds      = false
  enable_dynamodb = true
  enable_s3       = true
  ec2_ebs_rule = {
    selection = {
      resource_arns  = []
      selection_tags = [{ type = "STRINGEQUALS", key = "backup", value = "true" }]
    }
  }
  enable_ec2_ebs = false
  default_rule   = var.backups_default_rule
  project        = var.backups_project
  region         = var.backups_region
}

module "cloudwatchlogs" {
  source  = "../../aws/cloudwatchlogs"
  project = var.cloudwatchlogs_project
  region  = var.cloudwatchlogs_region
}

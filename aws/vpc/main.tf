terraform {
  required_version = ">= 1.5"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 6.0"
    }
  }
}

module "name" {
  source         = "github.com/luthersystems/tf-modules.git//luthername?ref=v55.15.0"
  luther_project = var.project
  aws_region     = var.region
  luther_env     = var.environment
  org_name       = "luthersystems"
  component      = "insideout"
  subcomponent   = "vpc"
  resource       = "vpc"
}

data "aws_availability_zones" "available" {
  state = "available"
}

data "aws_region" "current" {}

locals {
  # Use the first N AZs for subnets
  azs = slice(data.aws_availability_zones.available.names, 0, var.az_count)

  # Derive non-overlapping /20s for private/public subnets inside the /16 VPC by default.
  private_subnet_cidrs = [
    for i in range(length(local.azs)) : cidrsubnet(var.vpc_cidr, 4, i)
  ]

  public_subnet_cidrs = [
    for i in range(length(local.azs)) : cidrsubnet(var.vpc_cidr, 4, i + 8)
  ]

  eks_cluster_tag = var.eks_cluster_name != null ? { "kubernetes.io/cluster/${var.eks_cluster_name}" = "shared" } : {}
}

module "this" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "~> 6.0"

  name = module.name.name
  cidr = var.vpc_cidr
  azs  = local.azs

  private_subnets = local.private_subnet_cidrs
  public_subnets  = local.public_subnet_cidrs

  enable_dns_support   = true
  enable_dns_hostnames = true

  # NAT/IGW
  enable_nat_gateway = true
  single_nat_gateway = var.single_nat_gateway

  enable_vpn_gateway = false

  # Public subnets should auto-assign public IPs
  map_public_ip_on_launch = true

  public_subnet_tags = merge({ "kubernetes.io/role/elb" = "1" }, local.eks_cluster_tag)

  private_subnet_tags = merge({ "kubernetes.io/role/internal-elb" = "1" }, local.eks_cluster_tag)

  tags = merge(module.name.tags, var.tags)
}

# Optional: S3 Gateway Endpoint for private subnets (no Internet needed for S3)
resource "aws_vpc_endpoint" "s3" {
  vpc_id            = module.this.vpc_id
  service_name      = "com.amazonaws.${data.aws_region.current.region}.s3"
  vpc_endpoint_type = "Gateway"
  route_table_ids   = module.this.private_route_table_ids

  tags = merge(module.name.tags, { Name = "${module.name.prefix}-s3-endpoint" }, var.tags)
}

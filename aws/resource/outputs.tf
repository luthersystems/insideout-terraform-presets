output "cluster_name" {
  description = "EKS cluster name"
  value       = module.eks.cluster_name
}

output "cluster_version" {
  description = "Kubernetes version"
  value       = module.eks.cluster_version
}

output "cluster_endpoint" {
  description = "EKS API endpoint"
  value       = module.eks.cluster_endpoint
}

output "oidc_provider_arn" {
  description = "OIDC provider ARN"
  value       = module.eks.oidc_provider_arn
}

output "region" {
  description = "AWS region"
  value       = var.region
}

# Pass-throughs for convenience
output "vpc_id" {
  description = "VPC used by EKS"
  value       = var.vpc_id
}

output "private_subnets" {
  description = "Private subnets used by EKS"
  value       = var.private_subnet_ids
}

output "public_subnets" {
  description = "Public subnets (if provided)"
  value       = var.public_subnet_ids
}

output "cluster_arn" {
  description = "EKS cluster ARN"
  value       = module.eks.cluster_arn
}

output "ebs_csi_role_arn" {
  description = "IAM role ARN for the EBS CSI driver"
  value       = var.enable_ebs_csi_driver ? aws_iam_role.ebs_csi[0].arn : null
}

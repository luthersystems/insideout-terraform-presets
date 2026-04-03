output "cluster_name" {
  description = "Name of the ECS cluster"
  value       = aws_ecs_cluster.this.name
}

output "cluster_arn" {
  description = "ARN of the ECS cluster"
  value       = aws_ecs_cluster.this.arn
}

output "cluster_id" {
  description = "ID of the ECS cluster"
  value       = aws_ecs_cluster.this.id
}

output "service_connect_namespace" {
  description = "Service Connect namespace ARN (null if disabled)"
  value       = var.enable_service_connect ? aws_service_discovery_private_dns_namespace.this[0].arn : null
}

output "region" {
  description = "AWS region (passthrough)"
  value       = var.region
}

output "vpc_id" {
  description = "VPC ID (passthrough)"
  value       = var.vpc_id
}

output "private_subnet_ids" {
  description = "Private subnet IDs (passthrough for downstream services)"
  value       = var.private_subnet_ids
}

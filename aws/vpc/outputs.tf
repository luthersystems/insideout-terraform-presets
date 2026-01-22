output "vpc_id" {
  description = "The VPC ID"
  value       = module.this.vpc_id
}

output "private_subnet_ids" {
  description = "Private subnet IDs"
  value       = module.this.private_subnets
}

output "public_subnet_ids" {
  description = "Public subnet IDs"
  value       = module.this.public_subnets
}

output "private_route_table_ids" {
  description = "Private route table IDs"
  value       = module.this.private_route_table_ids
}

output "s3_gateway_endpoint_id" {
  description = "S3 VPC Gateway endpoint ID (if created)"
  value       = try(aws_vpc_endpoint.s3.id, null)
}

output "azs" {
  description = "Availability zones used by this VPC"
  value       = local.azs
}

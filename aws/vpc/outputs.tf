output "vpc_id" {
  description = "The VPC ID"
  value       = module.this.vpc_id

  # Cross-variable invariant: NAT requires private subnets. The upstream
  # terraform-aws-modules/vpc/aws otherwise plans aws_route.private_nat_gateway
  # against an empty aws_route_table.private and apply fails at the element()
  # call with "cannot use element function with an empty list" (issue #389).
  #
  # Preset-level backstop for the composer's mapper coercion in
  # pkg/composer/mapper.go — fires at terraform plan for any consumer of this
  # preset (composer or hand-authored) that emits the inconsistent pair.
  # Attached to the output rather than a separate terraform_data resource
  # because the latter would force the composer's required_providers
  # discovery to emit the built-in `terraform` provider, which has no source.
  precondition {
    condition     = !(var.enable_nat_gateway && !var.enable_private_subnets)
    error_message = "enable_nat_gateway=true requires enable_private_subnets=true: NAT routes attach to the private route table, which is empty when private subnets are disabled (issue #389)."
  }
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
  value       = try(aws_vpc_endpoint.s3[0].id, null)
}

output "azs" {
  description = "Availability zones used by this VPC"
  value       = local.azs
}

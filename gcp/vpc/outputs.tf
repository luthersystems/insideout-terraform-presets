output "vpc_id" {
  description = "The VPC network ID"
  value       = module.vpc.network_id
}

output "network_name" {
  description = "The VPC network name"
  value       = module.vpc.network_name
}

output "network_self_link" {
  description = "The VPC network self link"
  value       = module.vpc.network_self_link
}

output "subnet_ids" {
  description = "Subnet IDs"
  value       = module.vpc.subnets_ids
}

output "subnet_self_links" {
  description = "Subnet self links"
  value       = module.vpc.subnets_self_links
}

output "subnet_names" {
  description = "Subnet names"
  value       = module.vpc.subnets_names
}

output "pods_range_name" {
  description = "Name of the secondary range for GKE pods"
  value       = var.gke_cluster_name != null ? "${var.project}-pods" : null
}

output "services_range_name" {
  description = "Name of the secondary range for GKE services"
  value       = var.gke_cluster_name != null ? "${var.project}-services" : null
}

output "router_name" {
  description = "Cloud Router name (if Cloud NAT enabled)"
  value       = var.enable_cloud_nat ? google_compute_router.router[0].name : null
}

output "region" {
  description = "Region used by this VPC"
  value       = var.region
}


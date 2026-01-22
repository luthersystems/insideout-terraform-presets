output "cluster_id" {
  description = "The cluster ID"
  value       = module.gke.cluster_id
}

output "cluster_name" {
  description = "The cluster name"
  value       = module.gke.name
}

output "cluster_endpoint" {
  description = "The cluster API endpoint"
  value       = module.gke.endpoint
  sensitive   = true
}

output "cluster_ca_certificate" {
  description = "The cluster CA certificate (base64 encoded)"
  value       = module.gke.ca_certificate
  sensitive   = true
}

output "location" {
  description = "The cluster location (region or zone)"
  value       = module.gke.location
}

output "master_version" {
  description = "The Kubernetes master version"
  value       = module.gke.master_version
}

output "service_account" {
  description = "The default service account used by nodes"
  value       = module.gke.service_account
}

output "identity_namespace" {
  description = "Workload Identity namespace"
  value       = var.enable_workload_identity ? "${var.project}.svc.id.goog" : null
}


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
  value       = var.enable_workload_identity ? "${var.project_id}.svc.id.goog" : null
}

output "gpu_node_pool" {
  description = "Resolved GPU accelerator config for the default node pool (#767). enabled=false and count=0 when no GPU is attached. type/count are the guest_accelerator attached to each node; driver_version drives GKE auto NVIDIA driver install."
  value = {
    enabled        = local._gpu_enabled
    type           = local._gpu_accelerator_type
    count          = local._gpu_accelerator_count
    driver_version = local._gpu_node_driver_version
  }
}


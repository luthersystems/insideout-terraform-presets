# GKE Cluster Module using terraform-google-kubernetes-engine
# https://github.com/terraform-google-modules/terraform-google-kubernetes-engine

locals {
  cluster_name = "${var.project}-${var.cluster_name}"
}

module "gke" {
  source  = "terraform-google-modules/kubernetes-engine/google//modules/private-cluster"
  version = "~> 33.0"

  project_id = var.project
  name       = local.cluster_name
  region     = var.region
  regional   = var.regional
  zones      = length(var.node_zones) > 0 ? var.node_zones : null

  network           = var.network_self_link
  subnetwork        = var.subnet_self_link
  ip_range_pods     = var.pods_range_name
  ip_range_services = var.services_range_name

  kubernetes_version = var.kubernetes_version
  release_channel    = var.release_channel

  # Private cluster settings
  enable_private_nodes    = var.enable_private_nodes
  enable_private_endpoint = var.enable_private_endpoint
  master_ipv4_cidr_block  = var.enable_private_nodes ? var.master_ipv4_cidr_block : null

  master_authorized_networks = var.master_authorized_networks

  # Workload Identity
  identity_namespace = var.enable_workload_identity ? "${var.project}.svc.id.goog" : null

  # Remove default node pool
  remove_default_node_pool = true
  initial_node_count       = 1

  # Cluster features
  horizontal_pod_autoscaling = true
  http_load_balancing        = true
  network_policy             = true
  datapath_provider          = "ADVANCED_DATAPATH" # Enable Dataplane V2

  # Logging and monitoring
  logging_service    = "logging.googleapis.com/kubernetes"
  monitoring_service = "monitoring.googleapis.com/kubernetes"

  cluster_resource_labels = var.labels

  node_pools = [
    {
      name               = var.node_pool_name
      machine_type       = var.machine_type
      node_count         = var.node_count
      min_count          = var.min_node_count
      max_count          = var.max_node_count
      disk_size_gb       = var.disk_size_gb
      disk_type          = var.disk_type
      preemptible        = var.preemptible
      auto_repair        = true
      auto_upgrade       = true
      enable_gcfs        = false
      enable_gvnic       = true
      image_type         = "COS_CONTAINERD"
      initial_node_count = var.node_count
    }
  ]

  node_pools_oauth_scopes = {
    all = [
      "https://www.googleapis.com/auth/cloud-platform",
    ]
  }

  node_pools_labels = {
    all = merge(
      {
        project = var.project
      },
      var.labels
    )
  }
}


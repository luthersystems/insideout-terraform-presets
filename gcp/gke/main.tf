# GKE Cluster Module using terraform-google-kubernetes-engine
# https://github.com/terraform-google-modules/terraform-google-kubernetes-engine

# Per-deploy suffix so retries after state loss don't 409 on the GKE
# cluster name (issue #159). GKE cluster names are limited to 40 chars.
resource "random_id" "suffix" {
  byte_length = 4

  lifecycle {
    # GPU machine-type compatibility (#767). VALIDATE, don't mask. Hosted on the
    # always-present suffix resource because the node pool is created inside the
    # vendored gke module (no local resource to attach a precondition to). An
    # accelerator attaches via the node pool accelerator config only on N1
    # machines; A2/A3/A4/G2/G4 bundle their GPU with the machine type (so an
    # explicit accelerator is invalid) and every other family takes none.
    precondition {
      condition     = !local._gpu_enabled || (local._is_n1_machine && !local._is_bundled_gpu_machine)
      error_message = "gpu_type is set but machine_type=${var.machine_type} cannot attach a GPU: GKE attaches accelerators via the node pool accelerator config only on N1 machines (e.g. n1-standard-4). A2/A3/A4/G2/G4 accelerator-optimized machines bundle their GPU with the machine type — use that machine type alone with no gpu_type."
    }
  }
}

locals {
  cluster_name = "${var.project}-${var.cluster_name}-${random_id.suffix.hex}"

  # GPU node pool (#767). A non-empty gpu_type requests an attached accelerator.
  _gpu_enabled = trimspace(var.gpu_type) != ""

  # Machine family = the part before the first "-" (n1-standard-4 -> n1). Mirrors
  # the composer machineFamily() derive; kept in lockstep by TestGCPGPUFamiliesDrift.
  _machine_family = lower(split("-", trimspace(var.machine_type))[0])

  # Accelerators attach to the node pool ONLY on N1 machines. A2/A3/A4/G2/G4
  # accelerator-optimized machines bundle their GPU with the machine type.
  _gpu_bundled_machine_families = ["a2", "a3", "a4", "a4x", "g2", "g4"]
  _is_n1_machine                = local._machine_family == "n1"
  _is_bundled_gpu_machine       = contains(local._gpu_bundled_machine_families, local._machine_family)

  # Resolved accelerator config fed into the node pool object below. Inert (count
  # 0, empty type/driver) when no GPU, so the module skips guest_accelerator and
  # the auto-driver config. Surfaced via the gpu_node_pool output for assertions
  # and downstream consumers.
  _gpu_accelerator_count   = local._gpu_enabled ? (var.gpu_count > 0 ? var.gpu_count : 1) : 0
  _gpu_accelerator_type    = local._gpu_enabled ? var.gpu_type : ""
  _gpu_node_driver_version = local._gpu_enabled ? var.gpu_driver_version : ""
}

module "gke" {
  source  = "terraform-google-modules/kubernetes-engine/google//modules/private-cluster"
  version = "~> 33.0"

  project_id = var.project_id
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

  # Workload Identity pool name MUST be the real GCP project ID — that's what
  # the pool resource at <project_id>.svc.id.goog actually is. Using the
  # naming prefix here would silently break Workload Identity bindings.
  identity_namespace = var.enable_workload_identity ? "${var.project_id}.svc.id.goog" : null

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

  cluster_resource_labels = merge({ project = var.project }, var.labels)

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

      # GPU accelerator (#767). The module emits guest_accelerator only when
      # accelerator_count > 0, and gpu_driver_installation_config only when
      # gpu_driver_version != "" — so a non-GPU pool leaves these inert.
      # gpu_driver_version drives GKE auto NVIDIA driver install (no in-cluster
      # device-plugin work, unlike EKS).
      accelerator_count  = local._gpu_accelerator_count
      accelerator_type   = local._gpu_accelerator_type
      gpu_driver_version = local._gpu_node_driver_version
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


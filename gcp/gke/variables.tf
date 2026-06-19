variable "project" {
  description = "Naming/label prefix for stack resources (NOT a GCP project ID — see var.project_id)."
  type        = string

  validation {
    condition     = length(trimspace(var.project)) > 0
    error_message = "project must be a non-empty string."
  }
}

variable "project_id" {
  description = "Real GCP project ID where resources are created (e.g. \"my-prod-12345\"). Distinct from var.project, which is the naming/label prefix and need not be a valid GCP project ID."
  type        = string

  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{4,28}[a-z0-9]$", var.project_id))
    error_message = "project_id must be a valid GCP project ID: 6–30 chars, lowercase letters/digits/hyphens, start with a letter, end alphanumeric."
  }
}

variable "region" {
  description = "GCP region"
  type        = string
  default     = "us-central1"
}

variable "cluster_name" {
  description = "Name of the GKE cluster (without project prefix or random suffix). The composed name is <project>-<cluster_name>-<8hex>; GKE caps cluster names at 40 chars total, so cluster_name must leave room for the project prefix and the 9-char suffix."
  type        = string
  default     = "main"

  validation {
    condition     = length(var.cluster_name) <= 14
    error_message = "cluster_name must be ≤ 14 chars. GKE caps cluster names at 40 chars; the module composes <project>-<cluster_name>-<8hex>. Assuming a 15-char project prefix (typical InsideOut session ID), 14 chars here keeps the total at 40."
  }
}

variable "network_self_link" {
  description = "VPC network self link"
  type        = string
}

variable "subnet_self_link" {
  description = "Subnet self link"
  type        = string
}

variable "pods_range_name" {
  description = "Name of the secondary range for pods"
  type        = string
}

variable "services_range_name" {
  description = "Name of the secondary range for services"
  type        = string
}

variable "kubernetes_version" {
  description = "Kubernetes version (use 'latest' for latest available)"
  type        = string
  default     = "latest"
}

variable "release_channel" {
  description = "Release channel (RAPID, REGULAR, STABLE)"
  type        = string
  default     = "REGULAR"

  validation {
    condition     = contains(["RAPID", "REGULAR", "STABLE", "UNSPECIFIED"], var.release_channel)
    error_message = "release_channel must be one of: RAPID, REGULAR, STABLE, UNSPECIFIED."
  }
}

variable "regional" {
  description = "Create a regional (multi-zone) cluster for HA"
  type        = bool
  default     = true
}

variable "node_zones" {
  description = "Zones for nodes (only used if regional=true)"
  type        = list(string)
  default     = []
}

variable "enable_private_nodes" {
  description = "Enable private nodes (no public IPs)"
  type        = bool
  default     = true
}

variable "enable_private_endpoint" {
  description = "Enable private control plane endpoint"
  type        = bool
  default     = false
}

variable "master_ipv4_cidr_block" {
  description = "CIDR for the control plane (only for private clusters)"
  type        = string
  default     = "172.16.0.0/28"
}

variable "master_authorized_networks" {
  description = "Networks authorized to access the control plane"
  type = list(object({
    cidr_block   = string
    display_name = string
  }))
  default = []
}

variable "node_pool_name" {
  description = "Name of the default node pool"
  type        = string
  default     = "default"
}

variable "node_count" {
  description = "Initial node count per zone"
  type        = number
  default     = 1
  validation {
    condition     = var.node_count >= 1
    error_message = "node_count must be >= 1."
  }
}

variable "min_node_count" {
  description = "Minimum nodes per zone for autoscaling"
  type        = number
  default     = 1
}

variable "max_node_count" {
  description = "Maximum nodes per zone for autoscaling"
  type        = number
  default     = 3
}

variable "machine_type" {
  description = "Machine type for nodes. For a GPU node pool, use an N1 machine (e.g. n1-standard-4, attaches T4/V100/P100/P4) or an accelerator-optimized machine (a2-*/a3-*/a4-*/g2-*/g4-*, whose accelerator the node pool declares — paired with the family's GPU type). Unlike a Compute VM, a GKE node pool DECLARES the accelerator even for the accelerator-optimized families."
  type        = string
  default     = "e2-standard-4"
}

variable "gpu_type" {
  description = "NVIDIA accelerator type the node pool declares (e.g. nvidia-tesla-t4 on N1, nvidia-l4 on g2, nvidia-tesla-a100 on a2, nvidia-h100-80gb on a3). Empty = no GPU. Must pair with the machine family. When set, GKE auto-installs the NVIDIA driver (gpu_driver_version=DEFAULT) — no in-cluster device-plugin work needed, unlike EKS. GPU types are zone-constrained and quota-gated — a deploy-time operator concern."
  type        = string
  default     = ""
}

variable "gpu_count" {
  description = "Number of GPUs of gpu_type per node. Ignored unless gpu_type is set. Valid counts are 1, 2, 4, 8, or 16 (0 = no GPU). The exact legal count is GPU-type/zone/machine-specific (e.g. T4: 1/2/4; a2-highgpu-8g exposes 8) — a deploy-time/quota concern — but counts outside this set are always rejected by GCP."
  type        = number
  default     = 0

  validation {
    condition     = contains([0, 1, 2, 4, 8, 16], var.gpu_count)
    error_message = "gpu_count must be one of 0, 1, 2, 4, 8, 16."
  }
}

variable "gpu_driver_version" {
  description = "GKE GPU driver auto-install version: DEFAULT (driver for the node GKE version), LATEST, or INSTALLATION_DISABLED. Applied only when gpu_type is set."
  type        = string
  default     = "DEFAULT"

  validation {
    condition     = contains(["DEFAULT", "LATEST", "INSTALLATION_DISABLED"], var.gpu_driver_version)
    error_message = "gpu_driver_version must be one of: DEFAULT, LATEST, INSTALLATION_DISABLED."
  }
}

variable "disk_size_gb" {
  description = "Disk size for nodes in GB"
  type        = number
  default     = 100
}

variable "disk_type" {
  description = "Disk type for nodes"
  type        = string
  default     = "pd-standard"
}

variable "preemptible" {
  description = "Use preemptible (spot) nodes"
  type        = bool
  default     = false
}

variable "enable_workload_identity" {
  description = "Enable Workload Identity"
  type        = bool
  default     = true
}

variable "labels" {
  description = "Labels to apply to the cluster"
  type        = map(string)
  default     = {}
}

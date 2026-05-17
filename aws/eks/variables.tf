variable "project" {
  description = "Logical project name, used for naming and tags"
  type        = string

  validation {
    condition     = length(trimspace(var.project)) > 0
    error_message = "project must be a non-empty string."
  }
}

variable "environment" {
  description = "Deployment environment (e.g. production, staging, sandbox)"
  type        = string
  validation {
    condition     = length(trimspace(var.environment)) > 0
    error_message = "environment must be a non-empty string."
  }
}

variable "region" {
  description = "AWS region for the EKS control plane provider"
  type        = string

  validation {
    condition     = length(trimspace(var.region)) > 0
    error_message = "region must be a non-empty string (e.g., us-west-2)."
  }
}

variable "vpc_id" {
  description = "VPC ID for the EKS cluster"
  type        = string

  validation {
    condition     = length(trimspace(var.vpc_id)) > 0
    error_message = "vpc_id must be a non-empty string (e.g., vpc-xxxxxxxx)."
  }
}

variable "private_subnet_ids" {
  description = "Private subnet IDs for control plane and cluster endpoints"
  type        = list(string)

  validation {
    condition     = length(var.private_subnet_ids) >= 2
    error_message = "private_subnet_ids must include at least two subnet IDs in distinct AZs."
  }
}

# Optional: kept only so the root module can pass it without error.
# Not used by this module directly (node group module uses it).
variable "public_subnet_ids" {
  description = "Public subnet IDs (kept for interface parity; not used here)"
  type        = list(string)
  default     = []
}

variable "cluster_version" {
  description = "Kubernetes version for the EKS cluster"
  type        = string
  # Keep your existing default to match presets; change if desired.
  default = "1.33"

  # Accept versions like "1.29", "1.30", etc. (major.minor)
  validation {
    condition     = can(regex("^[0-9]+\\.[0-9]+$", var.cluster_version))
    error_message = "cluster_version must be in the form \"MAJOR.MINOR\" (e.g., \"1.29\")."
  }
}

variable "eks_public_control_plane" {
  description = "Whether the EKS API endpoint is publicly accessible"
  type        = bool
  default     = true
}

variable "cluster_enabled_log_types" {
  description = "EKS control plane log types to enable"
  type        = list(string)
  default     = ["api", "audit", "authenticator", "controllerManager", "scheduler"]

  # Ensure all entries are from the allowed set.
  validation {
    condition = length([
      for x in var.cluster_enabled_log_types : x
      if contains(["api", "audit", "authenticator", "controllerManager", "scheduler"], x)
    ]) == length(var.cluster_enabled_log_types)
    error_message = "cluster_enabled_log_types must be a subset of [api, audit, authenticator, controllerManager, scheduler]."
  }
}

variable "tags" {
  description = "Additional AWS tags applied to all resources"
  type        = map(string)
  default     = {}
}

# ---------------------------------------------------------------------------
# Addon toggles
# ---------------------------------------------------------------------------

variable "enable_coredns" {
  description = "Whether to install the CoreDNS addon"
  type        = bool
  default     = true
}

variable "enable_kube_proxy" {
  description = "Whether to install the kube-proxy addon"
  type        = bool
  default     = true
}

variable "enable_ebs_csi_driver" {
  description = "Whether to install the EBS CSI driver addon"
  type        = bool
  default     = true
}

variable "enable_ebs_csi_volume_modification" {
  description = "Enable the EBS CSI volume modification feature on the controller"
  type        = bool
  default     = true
}

# ---------------------------------------------------------------------------
# Addon version overrides (null = use built-in version map for cluster_version)
# ---------------------------------------------------------------------------

variable "addon_vpc_cni_version" {
  description = "Override VPC CNI addon version (null = auto-select for cluster_version)"
  type        = string
  default     = null
}

variable "addon_kube_proxy_version" {
  description = "Override kube-proxy addon version (null = auto-select for cluster_version)"
  type        = string
  default     = null
}

variable "addon_coredns_version" {
  description = "Override CoreDNS addon version (null = auto-select for cluster_version)"
  type        = string
  default     = null
}

variable "addon_ebs_csi_version" {
  description = "Override EBS CSI driver addon version (null = auto-select for cluster_version)"
  type        = string
  default     = null
}

# ---------------------------------------------------------------------------
# Addon timeouts
# ---------------------------------------------------------------------------

variable "addons_timeouts" {
  description = "Timeout configuration for EKS addon create/update/delete operations"
  type = object({
    create = optional(string)
    update = optional(string)
    delete = optional(string)
  })
  default = {}
}

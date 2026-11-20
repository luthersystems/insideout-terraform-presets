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

variable "container_insights_log_groups" {
  description = "Make the Container Insights log groups (/aws/containerinsights/<cluster>/*) that the amazon-cloudwatch-observability addon of aws/eks_nodegroup writes to, so a destroy deletes them (#957). Set false only when no node group runs the addon."
  type        = bool
  default     = true
}

variable "container_insights_log_retention_days" {
  description = "Days to keep the Container Insights logs in CloudWatch Logs. Must be one of CloudWatch's retention values."
  type        = number
  default     = 30
  validation {
    condition     = contains([1, 3, 5, 7, 14, 30, 60, 90, 120, 150, 180, 365, 400, 545, 731, 1096, 1827, 2192, 2557, 2922, 3288, 3653], var.container_insights_log_retention_days)
    error_message = "container_insights_log_retention_days must be one of CloudWatch's retention values: 1, 3, 5, 7, 14, 30, 60, 90, 120, 150, 180, 365, 400, 545, 731, 1096, 1827, 2192, 2557, 2922, 3288, 3653."
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

variable "iam_scope" {
  description = "IAM path and permissions boundary for the roles, customer managed policies and instance profiles this module creates. The scoped deploy policy allows IAM writes only under /insideout/<project>/ and creates roles only with the workload boundary (sandbox-infrastructure-template#147). Both are set together, because the boundary passes only roles on the path. null keeps the module's defaults. Changing it replaces the roles."
  type = object({
    path                 = string
    permissions_boundary = string
  })
  default = null

  validation {
    condition = var.iam_scope == null ? true : (
      can(regex("^/insideout/[A-Za-z0-9+=,.@_-]+/$", var.iam_scope.path)) &&
      can(regex("^arn:aws[a-z-]*:iam::([0-9]{12}|aws):policy/", var.iam_scope.permissions_boundary))
    )
    error_message = "iam_scope.path must be /insideout/<project>/ and iam_scope.permissions_boundary an IAM policy ARN."
  }
}

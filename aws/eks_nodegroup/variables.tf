variable "region" {
  description = "AWS region for this module"
  type        = string
  validation {
    condition     = length(trimspace(var.region)) > 0
    error_message = "region must be a non-empty string."
  }
}

variable "project" {
  description = "Project name used for tagging/naming"
  type        = string
  validation {
    condition     = length(trimspace(var.project)) > 0
    error_message = "project must be a non-empty string."
  }
}

variable "cluster_name" {
  description = "Name of the EKS cluster to attach the node group to"
  type        = string
  validation {
    condition     = length(trimspace(var.cluster_name)) > 0
    error_message = "cluster_name must be a non-empty string."
  }
}

variable "node_group_name" {
  description = "Name of the managed node group"
  type        = string
  default     = "default"
  validation {
    condition     = length(trimspace(var.node_group_name)) > 0
    error_message = "node_group_name must be a non-empty string."
  }
}

variable "subnet_ids" {
  description = "Subnets for the node group (typically private subnets)"
  type        = list(string)
  # Allow empty when composing this module standalone; if non-empty, require non-empty items
  validation {
    condition     = length(var.subnet_ids) == 0 || alltrue([for s in var.subnet_ids : length(trimspace(s)) > 0])
    error_message = "subnet_ids may be an empty list for standalone composition, but any provided values must be non-empty strings."
  }
}

variable "instance_types" {
  description = "Instance types for the node group (e.g., [\"c7i.large\"])"
  type        = list(string)
  validation {
    condition     = length(var.instance_types) >= 1 && alltrue([for t in var.instance_types : length(trimspace(t)) > 0])
    error_message = "instance_types must include at least one non-empty EC2 type (e.g., \"c7i.large\")."
  }
}

variable "desired_size" {
  description = "Desired number of nodes"
  type        = number
  validation {
    condition     = var.desired_size >= 0
    error_message = "desired_size must be >= 0."
  }
}

variable "min_size" {
  description = "Minimum number of nodes"
  type        = number
  validation {
    condition     = var.min_size >= 0
    error_message = "min_size must be >= 0."
  }
}

variable "max_size" {
  description = "Maximum number of nodes"
  type        = number
  validation {
    condition     = var.max_size >= 0
    error_message = "max_size must be >= 0."
  }
}

variable "capacity_type" {
  description = "Capacity type for the node group (ON_DEMAND or SPOT). Leave null for provider default."
  type        = string
  default     = null
  validation {
    condition     = var.capacity_type == null ? true : contains(["ON_DEMAND", "SPOT"], var.capacity_type)
    error_message = "capacity_type must be null (use provider default) or one of: ON_DEMAND, SPOT."
  }
}

variable "labels" {
  description = "Kubernetes node labels to apply to the node group"
  type        = map(string)
  default     = {}
}

variable "tags" {
  description = "Additional AWS tags applied to created resources"
  type        = map(string)
  default     = {}
}

variable "node_role_arn" {
  description = "Existing IAM role ARN for the node group. If null, this module will create one."
  type        = string
  default     = null
}

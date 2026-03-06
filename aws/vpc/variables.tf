variable "project" {
  description = "Project/name prefix"
  type        = string
  default     = "demo"

  validation {
    condition     = length(trimspace(var.project)) > 0
    error_message = "project must be a non-empty string."
  }
}

variable "region" {
  description = "AWS region"
  type        = string
  default     = "us-east-1"

  validation {
    condition     = length(trimspace(var.region)) > 0
    error_message = "region must be a non-empty string (e.g., us-west-2)."
  }
}

variable "vpc_cidr" {
  description = "CIDR block for the VPC (e.g., 10.1.0.0/16)"
  type        = string
  default     = "10.1.0.0/16"

  # Validate it looks like CIDR and Terraform can parse it
  validation {
    condition     = can(cidrnetmask(var.vpc_cidr))
    error_message = "vpc_cidr must be a valid IPv4 CIDR (e.g., 10.1.0.0/16)."
  }
}

variable "az_count" {
  description = "Number of AZs to use for subnets"
  type        = number
  default     = 2

  # We can’t reference other variables/data here; keep it simple and safe.
  validation {
    condition     = var.az_count >= 1
    error_message = "az_count must be at least 1 (2 is recommended for HA)."
  }
}

variable "enable_nat_gateway" {
  description = "Enable NAT gateway for private subnets (set false for public-only VPCs)"
  type        = bool
  default     = true
}

variable "single_nat_gateway" {
  description = "Use a single NAT gateway (cost saver) instead of one per AZ"
  type        = bool
  default     = true
}

variable "vpc_mode" {
  description = "VPC subnet mode: 'private' creates both private and public subnets, 'public' creates only public subnets"
  type        = string
  default     = "private"

  validation {
    condition     = contains(["public", "private"], var.vpc_mode)
    error_message = "vpc_mode must be either 'public' or 'private'."
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

variable "eks_cluster_name" {
  description = "If set, tag subnets for this EKS cluster: kubernetes.io/cluster/<name>=shared"
  type        = string
  default     = null

  # If provided (non-null), it must not be an empty/whitespace-only string.
  validation {
    condition     = var.eks_cluster_name == null ? true : length(trimspace(var.eks_cluster_name)) > 0
    error_message = "eks_cluster_name, when provided, must be a non-empty string."
  }
}

variable "tags" {
  description = "Additional AWS tags applied to all resources"
  type        = map(string)
  default     = {}
}

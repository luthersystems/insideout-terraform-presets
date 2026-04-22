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
  description = "Number of AZs to span with public+private subnets. Also bounds the number of NAT gateways when single_nat_gateway=false. 2 is recommended for HA."
  type        = number
  default     = 2

  # We can’t reference other variables/data here; keep it simple and safe.
  validation {
    condition     = var.az_count >= 1
    error_message = "az_count must be at least 1 (2 is recommended for HA)."
  }
}

variable "enable_nat_gateway" {
  description = "Enable NAT gateways so private subnets can reach the public internet. Set false only for public-only VPCs (no private workloads). Topology (one vs one-per-AZ) is controlled by single_nat_gateway."
  type        = bool
  default     = true
}

variable "single_nat_gateway" {
  description = <<-EOT
    Provision exactly one NAT gateway (in the first public subnet / first AZ) shared by all private subnets,
    instead of one NAT gateway per AZ. Defaults to true for cost.

    - true (default): cheapest, ~1/N the NAT cost, but (a) single point of failure if that AZ goes down
      and (b) every stack in the account lands its NAT in the first AZ — accounts running multiple stacks
      can exhaust the per-AZ NAT gateway quota (default 5) before running out of the VPC quota.
    - false: one NAT gateway per AZ (as bounded by az_count). ~N× cost, no per-AZ SPOF, spreads NAT
      quota usage across AZs. Recommended once an account runs more than a handful of concurrent VPCs
      or whenever AZ-level availability is a requirement.
  EOT
  type        = bool
  default     = true
}

variable "enable_private_subnets" {
  description = "Create private subnets alongside public subnets (set false for public-only VPCs)"
  type        = bool
  default     = true
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

variable "region" {
  description = "AWS region (e.g., us-east-1)"
  type        = string
  validation {
    condition     = length(trimspace(var.region)) > 0
    error_message = "region must be a non-empty string."
  }
}

variable "project" {
  description = "Name prefix for resources"
  type        = string
  default     = "demo"
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

variable "vpc_id" {
  description = "Target VPC ID"
  type        = string
  validation {
    condition     = length(trimspace(var.vpc_id)) > 0
    error_message = "vpc_id must be a non-empty string."
  }
}

variable "subnet_id" {
  description = "Public subnet ID for the bastion instance"
  type        = string
  validation {
    condition     = length(trimspace(var.subnet_id)) > 0
    error_message = "subnet_id must be a non-empty string."
  }
}

variable "admin_cidrs" {
  description = "CIDR blocks allowed to SSH (port 22) to the bastion. Keep it tight in real environments. An empty list removes the SSH rule, for a bastion reached only through SSM Session Manager. Default 0.0.0.0/0 keeps the current open rule."
  type        = list(string)
  default     = ["0.0.0.0/0"]
  validation {
    # The same per-CIDR rule as egress_cidrs in aws/alb (#907), without
    # its length check, because an empty list is the SSM-only opt-in.
    # IPv4 only (cidr_blocks takes no IPv6), and each CIDR must be its
    # network address: AWS stores 10.0.0.1/16 as 10.0.0.0/16, so the plan
    # would never settle. The composer's offline validator evaluates this
    # rule at compose time too, so use only the functions it knows
    # (pkg/composer/hcl_validation.go).
    condition = alltrue([
      for c in var.admin_cidrs : can(regex("^[0-9]{1,3}(\\.[0-9]{1,3}){3}/[0-9]{1,2}$", c)) && can(cidrhost(c, 0)) ? cidrhost(c, 0) == regex("^[^/]+", c) : false
    ])
    error_message = "admin_cidrs must contain IPv4 CIDR blocks, each written as its network address (203.0.113.0/24, not 203.0.113.7/24)."
  }
}

variable "arch" {
  description = "CPU architecture for the AMI"
  type        = string
  default     = "arm64"
  validation {
    condition     = contains(["arm64", "x86_64"], var.arch)
    error_message = "arch must be one of: arm64, x86_64."
  }
}

variable "instance_type" {
  description = "EC2 instance type for the bastion (t4g.* for ARM, t3.* for x86)"
  type        = string
  default     = "t4g.nano"
  validation {
    condition     = length(trimspace(var.instance_type)) > 0
    error_message = "instance_type must be a non-empty string."
  }
}

variable "key_name" {
  description = "Existing EC2 key pair name for SSH (null to rely on SSM only)"
  type        = string
  default     = null
}

variable "install_eks_tools" {
  description = "Install awscli/kubectl/eksctl for cluster admin from the bastion"
  type        = bool
  default     = true
}

variable "tags" {
  description = "Common resource tags"
  type        = map(string)
  default     = {}
}

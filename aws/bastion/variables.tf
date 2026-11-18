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
    condition     = alltrue([for c in var.admin_cidrs : can(cidrhost(c, 0))])
    error_message = "admin_cidrs must contain valid CIDR blocks."
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

variable "iam_path" {
  description = "IAM path for the roles, customer managed policies and instance profiles this module creates. The scoped deploy policy allows IAM writes only under /insideout/<project>/ (sandbox-infrastructure-template#147). null keeps the module's default path. Changing it replaces the roles."
  type        = string
  default     = null

  validation {
    condition     = var.iam_path == null ? true : can(regex("^/([A-Za-z0-9+=,.@_-]+/)*$", var.iam_path))
    error_message = "iam_path must start and end with / and hold only IAM path characters."
  }
}

variable "iam_permissions_boundary" {
  description = "ARN of the permissions boundary for every role this module creates. The scoped deploy policy refuses a role without the workload boundary (sandbox-infrastructure-template#147). null sets no boundary."
  type        = string
  default     = null

  validation {
    condition     = var.iam_permissions_boundary == null ? true : can(regex("^arn:aws[a-z-]*:iam::([0-9]{12}|aws):policy/", var.iam_permissions_boundary))
    error_message = "iam_permissions_boundary must be an IAM policy ARN."
  }
}

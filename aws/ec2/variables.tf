variable "region" {
  description = "AWS region (e.g., us-east-1)"
  type        = string
  validation {
    condition     = length(trimspace(var.region)) > 0
    error_message = "region must be a non-empty string."
  }
}

variable "project" {
  description = "Project name used for resource naming and tagging"
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

variable "vpc_id" {
  description = "VPC ID for the security group"
  type        = string
  validation {
    condition     = length(trimspace(var.vpc_id)) > 0
    error_message = "vpc_id must be a non-empty string."
  }
}

variable "subnet_id" {
  description = "Subnet ID for instance placement"
  type        = string
  validation {
    condition     = length(trimspace(var.subnet_id)) > 0
    error_message = "subnet_id must be a non-empty string."
  }
}

variable "instance_type" {
  description = "EC2 instance type"
  type        = string
  default     = "t3.medium"
  validation {
    condition     = length(trimspace(var.instance_type)) > 0
    error_message = "instance_type must be a non-empty string."
  }
}

variable "ami_id" {
  description = "Specific AMI ID to use. If null, the latest Amazon Linux 2023 AMI for the given arch is selected."
  type        = string
  default     = null
}

variable "arch" {
  description = "CPU architecture for AMI lookup when ami_id is null"
  type        = string
  default     = "x86_64"
  validation {
    condition     = contains(["arm64", "x86_64"], var.arch)
    error_message = "arch must be one of: arm64, x86_64."
  }
}

variable "key_name" {
  description = "Existing EC2 key pair name for SSH (null to rely on SSM only)"
  type        = string
  default     = null
}

variable "ssh_public_key" {
  description = "SSH public key material (e.g., 'ssh-ed25519 AAAA...'). Creates an EC2 key pair when provided."
  type        = string
  default     = ""
}

variable "associate_public_ip" {
  description = "Whether to associate a public IP address with the instance"
  type        = bool
  default     = false
}

variable "user_data" {
  description = "User data script to run on instance launch (plain text, provider handles encoding). Mutually exclusive with user_data_url."
  type        = string
  default     = ""
}

variable "user_data_url" {
  description = "URL of a shell script to download and execute on instance launch. Generates a wrapper that fetches and runs the script. Mutually exclusive with user_data."
  type        = string
  default     = ""
}

variable "custom_ingress_ports" {
  description = "List of TCP port numbers to open for ingress on the security group"
  type        = list(number)
  default     = []
  validation {
    condition     = alltrue([for p in var.custom_ingress_ports : p >= 1 && p <= 65535])
    error_message = "Each port in custom_ingress_ports must be between 1 and 65535."
  }
}

variable "ingress_cidr_blocks" {
  description = "CIDR blocks allowed for custom ingress rules"
  type        = list(string)
  default     = ["0.0.0.0/0"]
  validation {
    condition     = alltrue([for c in var.ingress_cidr_blocks : can(cidrhost(c, 0))])
    error_message = "ingress_cidr_blocks must contain valid CIDR blocks."
  }
}

variable "tags" {
  description = "Additional AWS tags applied to created resources"
  type        = map(string)
  default     = {}
}

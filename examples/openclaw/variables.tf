variable "environment" {
  description = "Deployment environment (e.g. production, staging, sandbox)"
  type        = string
  default     = "sandbox"
}

variable "project" {
  description = "Root project name, stamped into the provider default_tags block so every AWS resource carries a Project tag"
  type        = string
  default     = "openclaw"
}

variable "vpc_project" {
  description = "Project name for VPC. Keep in sync with var.project — the Project tag stamped in the provider default_tags block is read from var.project, not var.vpc_project, so a mismatch produces inconsistent tagging."
  type        = string
}

variable "vpc_region" {
  description = "AWS region for VPC"
  type        = string
}

variable "ec2_project" {
  description = "Project name for EC2 instance. Keep in sync with var.project; see vpc_project."
  type        = string
}

variable "ec2_region" {
  description = "AWS region for EC2 instance"
  type        = string
}

variable "ec2_ami_id" {
  description = "AMI ID (Ubuntu 24.04 recommended for OpenClaw)"
  type        = string
  default     = null
}

variable "ec2_instance_type" {
  description = "EC2 instance type"
  type        = string
  default     = "t3.medium"
}

variable "ec2_associate_public_ip" {
  description = "Whether to associate a public IP with the EC2 instance"
  type        = bool
  default     = true
}

variable "ec2_ssh_public_key" {
  description = "SSH public key material for EC2 access (e.g., 'ssh-ed25519 AAAA...')"
  type        = string
  default     = ""
}

variable "ec2_user_data" {
  description = "User data script for the EC2 instance"
  type        = string
  default     = ""
}

variable "ec2_custom_ingress_ports" {
  description = "TCP ports to open for ingress"
  type        = list(number)
  default     = []
}

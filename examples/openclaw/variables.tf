variable "vpc_project" {
  description = "Project name for VPC"
  type        = string
}

variable "vpc_region" {
  description = "AWS region for VPC"
  type        = string
}

variable "ec2_project" {
  description = "Project name for EC2 instance"
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

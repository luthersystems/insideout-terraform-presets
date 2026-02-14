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

variable "ec2_ingress_cidr_blocks" {
  description = "CIDR blocks allowed for custom ingress rules"
  type        = list(string)
  default     = ["0.0.0.0/0"]
}

module "vpc" {
  source  = "../../aws/vpc"
  project = var.vpc_project
  region  = var.vpc_region
}

module "ec2" {
  source               = "../../aws/ec2"
  vpc_id               = module.vpc.vpc_id
  subnet_id            = module.vpc.public_subnet_ids[0]
  ami_id               = var.ec2_ami_id
  associate_public_ip  = var.ec2_associate_public_ip
  instance_type        = var.ec2_instance_type
  ssh_public_key       = var.ec2_ssh_public_key
  user_data            = var.ec2_user_data
  custom_ingress_ports = var.ec2_custom_ingress_ports
  project              = var.ec2_project
  region               = var.ec2_region
}

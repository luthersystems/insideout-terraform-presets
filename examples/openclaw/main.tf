module "vpc" {
  source  = "../../aws/vpc"
  project = var.vpc_project
  region  = var.vpc_region
}

module "ec2" {
  source               = "../../aws/ec2"
  vpc_id               = module.vpc.vpc_id
  subnet_id            = module.vpc.public_subnet_ids[0]
  associate_public_ip  = var.ec2_associate_public_ip
  instance_type        = var.ec2_instance_type
  user_data            = var.ec2_user_data
  custom_ingress_ports = var.ec2_custom_ingress_ports
  ingress_cidr_blocks  = var.ec2_ingress_cidr_blocks
  project              = var.ec2_project
  region               = var.ec2_region
}

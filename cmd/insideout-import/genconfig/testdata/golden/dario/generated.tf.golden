# __generated__ by Terraform
# Please review these resources and move them into your main configuration files.

# __generated__ by Terraform
resource "aws_kms_alias" "alias_aws_backup" {
  name          = "alias/aws/backup"
  region        = "us-east-1"
  target_key_id = "1aba97aa-991e-4c8b-b878-ef325f09ba81"
}

# __generated__ by Terraform from "alias/7942cdea-tfstate@us-east-1"
resource "aws_kms_alias" "alias_7942cdea_tfstate" {
  name          = "alias/7942cdea-tfstate"
  region        = "us-east-1"
  target_key_id = "7cf67ec5-52d9-4fff-af9f-ab0202aa86cf"
}

# __generated__ by Terraform from "sg-0a4d015d2d0a8277a@us-east-1"
resource "aws_security_group" "default_dcde184c" {
  description            = "default VPC security group"
  egress                 = []
  ingress                = []
  name                   = "default"
  region                 = "us-east-1"
  revoke_rules_on_delete = null
  tags = {
    Component    = "insideout"
    Environment  = "prod"
    ID           = "vpc0"
    Name         = "io-f-v6e-hzw-zt-prod-luthersystems-insideout-vpc-vpc0"
    Organization = "luthersystems"
    Project      = "io-f-v6e-hzw-zt"
    Resource     = "vpc"
    Subcomponent = "vpc"
  }
  tags_all = {
    Component    = "insideout"
    Environment  = "prod"
    ID           = "vpc0"
    Name         = "io-f-v6e-hzw-zt-prod-luthersystems-insideout-vpc-vpc0"
    Organization = "luthersystems"
    Project      = "io-f-v6e-hzw-zt"
    Resource     = "vpc"
    Subcomponent = "vpc"
  }
  vpc_id = "vpc-0328efde06fc443f8"
}

# __generated__ by Terraform
resource "aws_subnet" "subnet_05c38ddcb284ea781" {
  assign_ipv6_address_on_creation                = false
  availability_zone                              = "us-east-1f"
  availability_zone_id                           = "use1-az5"
  cidr_block                                     = "172.31.64.0/20"
  customer_owned_ipv4_pool                       = null
  enable_dns64                                   = false
  enable_lni_at_device_index                     = 0
  enable_resource_name_dns_a_record_on_launch    = false
  enable_resource_name_dns_aaaa_record_on_launch = false
  ipv4_ipam_pool_id                              = null
  ipv4_netmask_length                            = null
  ipv6_ipam_pool_id                              = null
  ipv6_native                                    = false
  ipv6_netmask_length                            = null
  map_customer_owned_ip_on_launch                = false
  map_public_ip_on_launch                        = true
  outpost_arn                                    = null
  private_dns_hostname_type_on_launch            = "ip-name"
  region                                         = "us-east-1"
  tags                                           = {}
  tags_all                                       = {}
  vpc_id                                         = "vpc-0648c27c4d5455bc7"
}

# __generated__ by Terraform from "alias/55f50492-tfstate@us-east-1"
resource "aws_kms_alias" "alias_55f50492_tfstate" {
  name          = "alias/55f50492-tfstate"
  region        = "us-east-1"
  target_key_id = "9b00e98c-0de7-480e-bd81-ed2e84a5f1b9"
}

# __generated__ by Terraform from "alias/6ee757a6-tfstate@us-east-1"
resource "aws_kms_alias" "alias_6ee757a6_tfstate" {
  name          = "alias/6ee757a6-tfstate"
  region        = "us-east-1"
  target_key_id = "daa6ca9e-d68e-4998-bd6a-4c627b8ece08"
}

# __generated__ by Terraform
resource "aws_subnet" "subnet_06a0c2a29445047a9" {
  assign_ipv6_address_on_creation                = false
  availability_zone                              = "us-east-1e"
  availability_zone_id                           = "use1-az3"
  cidr_block                                     = "172.31.48.0/20"
  customer_owned_ipv4_pool                       = null
  enable_dns64                                   = false
  enable_lni_at_device_index                     = 0
  enable_resource_name_dns_a_record_on_launch    = false
  enable_resource_name_dns_aaaa_record_on_launch = false
  ipv4_ipam_pool_id                              = null
  ipv4_netmask_length                            = null
  ipv6_ipam_pool_id                              = null
  ipv6_native                                    = false
  ipv6_netmask_length                            = null
  map_customer_owned_ip_on_launch                = false
  map_public_ip_on_launch                        = true
  outpost_arn                                    = null
  private_dns_hostname_type_on_launch            = "ip-name"
  region                                         = "us-east-1"
  tags                                           = {}
  tags_all                                       = {}
  vpc_id                                         = "vpc-0648c27c4d5455bc7"
}

# __generated__ by Terraform
resource "aws_network_interface" "eni_0071933677a6be65f" {
  description               = null
  interface_type            = "interface"
  ipv4_prefix_count         = 0
  ipv4_prefixes             = []
  ipv6_address_count        = 0
  ipv6_address_list         = []
  ipv6_address_list_enabled = null
  ipv6_addresses            = []
  ipv6_prefix_count         = 0
  ipv6_prefixes             = []
  private_ip                = "10.1.134.121"
  private_ip_list           = ["10.1.134.121"]
  private_ip_list_enabled   = null
  private_ips               = ["10.1.134.121"]
  private_ips_count         = 0
  region                    = "us-east-1"
  security_groups           = ["sg-07ab0fd93782eb359"]
  source_dest_check         = true
  subnet_id                 = "subnet-0e866663c4c5ad4d9"
  tags                      = {}
  tags_all                  = {}
  attachment {
    device_index       = 0
    instance           = "i-0f489bc60a257a4f3"
    network_card_index = 0
  }
}

# __generated__ by Terraform
resource "aws_nat_gateway" "nat_08bcfea9265221ec4" {
  allocation_id                      = "eipalloc-07d114af86fd5d1c3"
  availability_mode                  = "zonal"
  connectivity_type                  = "public"
  private_ip                         = "10.1.143.246"
  region                             = "us-east-1"
  secondary_allocation_ids           = []
  secondary_private_ip_address_count = 0
  secondary_private_ip_addresses     = []
  subnet_id                          = "subnet-0e866663c4c5ad4d9"
  tags = {
    Component    = "insideout"
    Environment  = "prod"
    ID           = "vpc0"
    Name         = "io-f0ttnkgjdvee-prod-luthersystems-insideout-vpc-vpc0"
    Organization = "luthersystems"
    Project      = "io-f0ttnkgjdvee"
    Resource     = "vpc"
    Subcomponent = "vpc"
  }
  tags_all = {
    Component    = "insideout"
    Environment  = "prod"
    ID           = "vpc0"
    Name         = "io-f0ttnkgjdvee-prod-luthersystems-insideout-vpc-vpc0"
    Organization = "luthersystems"
    Project      = "io-f0ttnkgjdvee"
    Resource     = "vpc"
    Subcomponent = "vpc"
  }
  vpc_id = "vpc-0b5b7a95d51782af5"
}

# __generated__ by Terraform from "sgr-0fc0350a2ce276c3c@us-east-1"
resource "aws_vpc_security_group_ingress_rule" "sgr_0fc0350a2ce276c3c" {
  cidr_ipv4                    = "18.206.107.24/29"
  cidr_ipv6                    = null
  description                  = "SSH from EC2 Instance Connect service IPs"
  from_port                    = 22
  ip_protocol                  = "tcp"
  prefix_list_id               = null
  referenced_security_group_id = null
  region                       = "us-east-1"
  security_group_id            = "sg-07ab0fd93782eb359"
  tags                         = null
  to_port                      = 22
}

# __generated__ by Terraform
resource "aws_route_table" "rtb_02eb84aa4898e1f26" {
  propagating_vgws = []
  region           = "us-east-1"
  route = [{
    carrier_gateway_id         = ""
    cidr_block                 = "0.0.0.0/0"
    core_network_arn           = ""
    destination_prefix_list_id = ""
    egress_only_gateway_id     = ""
    gateway_id                 = "igw-0dd9d251e758fc090"
    ipv6_cidr_block            = ""
    local_gateway_id           = ""
    nat_gateway_id             = ""
    network_interface_id       = ""
    transit_gateway_id         = ""
    vpc_endpoint_id            = ""
    vpc_peering_connection_id  = ""
  }]
  tags = {
    Component    = "insideout"
    Environment  = "prod"
    ID           = "vpc0"
    Name         = "io-f0ttnkgjdvee-prod-luthersystems-insideout-vpc-vpc0"
    Organization = "luthersystems"
    Project      = "io-f0ttnkgjdvee"
    Resource     = "vpc"
    Subcomponent = "vpc"
  }
  tags_all = {
    Component    = "insideout"
    Environment  = "prod"
    ID           = "vpc0"
    Name         = "io-f0ttnkgjdvee-prod-luthersystems-insideout-vpc-vpc0"
    Organization = "luthersystems"
    Project      = "io-f0ttnkgjdvee"
    Resource     = "vpc"
    Subcomponent = "vpc"
  }
  vpc_id = "vpc-0b5b7a95d51782af5"
}

# __generated__ by Terraform from "84d279a0-or-default-app-profile-worker0"
resource "aws_iam_instance_profile" "r_84d279a0_or_default_app_profile_worker0" {
  name     = "84d279a0-or-default-app-profile-worker0"
  path     = "/"
  role     = null
  tags     = {}
  tags_all = {}
}

# __generated__ by Terraform
resource "aws_kms_key" "r_9b00e98c_0de7_480e_bd81_ed2e84a5f1b9" {
  bypass_policy_lockout_safety_check = null
  custom_key_store_id                = null
  customer_master_key_spec           = "SYMMETRIC_DEFAULT"
  deletion_window_in_days            = null
  description                        = "tfstate encryption key for 55f50492 default environment"
  enable_key_rotation                = false
  is_enabled                         = true
  key_usage                          = "ENCRYPT_DECRYPT"
  multi_region                       = false
  policy = jsonencode({
    Id = "key-default-1"
    Statement = [{
      Action = "kms:*"
      Effect = "Allow"
      Principal = {
        AWS = "arn:aws:iam::141812438321:root"
      }
      Resource = "*"
      Sid      = "Enable IAM User Permissions"
    }]
    Version = "2012-10-17"
  })
  region                  = "us-east-1"
  rotation_period_in_days = 0
  tags = {
    Component    = "tfstate"
    Environment  = "default"
    ID           = "0"
    Name         = "55f50492-default-luther-tfstate-kms-0"
    Organization = "luther"
    Project      = "55f50492"
    Resource     = "kms"
  }
  tags_all = {
    Component    = "tfstate"
    Environment  = "default"
    ID           = "0"
    Name         = "55f50492-default-luther-tfstate-kms-0"
    Organization = "luther"
    Project      = "55f50492"
    Resource     = "kms"
  }
  xks_key_id = null
}

# __generated__ by Terraform from "https://sqs.us-east-1.amazonaws.com/141812438321/io-f-v6e-hzw-zt-queue@us-east-1"
resource "aws_sqs_queue" "io_f_v6e_hzw_zt_queue" {
  content_based_deduplication       = false
  delay_seconds                     = 0
  fifo_queue                        = false
  kms_data_key_reuse_period_seconds = 300
  kms_master_key_id                 = null
  max_message_size                  = 262144
  message_retention_seconds         = 345600
  name                              = "io-f-v6e-hzw-zt-queue"
  receive_wait_time_seconds         = 10
  redrive_policy = jsonencode({
    deadLetterTargetArn = "arn:aws:sqs:us-east-1:141812438321:io-f-v6e-hzw-zt-queue-dlq"
    maxReceiveCount     = 5
  })
  region                  = "us-east-1"
  sqs_managed_sse_enabled = true
  tags = {
    Component    = "insideout"
    Environment  = "prod"
    ID           = "sqs0"
    Name         = "io-f-v6e-hzw-zt-prod-luthersystems-insideout-sqs-sqs0"
    Organization = "luthersystems"
    Project      = "io-f-v6e-hzw-zt"
    Resource     = "sqs"
    Subcomponent = "sqs"
  }
  tags_all = {
    Component    = "insideout"
    Environment  = "prod"
    ID           = "sqs0"
    Name         = "io-f-v6e-hzw-zt-prod-luthersystems-insideout-sqs-sqs0"
    Organization = "luthersystems"
    Project      = "io-f-v6e-hzw-zt"
    Resource     = "sqs"
    Subcomponent = "sqs"
  }
  visibility_timeout_seconds = 30
}

# __generated__ by Terraform
resource "aws_subnet" "subnet_0ce332104cc574eaa" {
  assign_ipv6_address_on_creation                = false
  availability_zone                              = "us-east-1c"
  availability_zone_id                           = "use1-az4"
  cidr_block                                     = "172.31.16.0/20"
  customer_owned_ipv4_pool                       = null
  enable_dns64                                   = false
  enable_lni_at_device_index                     = 0
  enable_resource_name_dns_a_record_on_launch    = false
  enable_resource_name_dns_aaaa_record_on_launch = false
  ipv4_ipam_pool_id                              = null
  ipv4_netmask_length                            = null
  ipv6_ipam_pool_id                              = null
  ipv6_native                                    = false
  ipv6_netmask_length                            = null
  map_customer_owned_ip_on_launch                = false
  map_public_ip_on_launch                        = true
  outpost_arn                                    = null
  private_dns_hostname_type_on_launch            = "ip-name"
  region                                         = "us-east-1"
  tags                                           = {}
  tags_all                                       = {}
  vpc_id                                         = "vpc-0648c27c4d5455bc7"
}

# __generated__ by Terraform from "1aba97aa-991e-4c8b-b878-ef325f09ba81@us-east-1"
resource "aws_kms_key" "r_1aba97aa_991e_4c8b_b878_ef325f09ba81" {
  bypass_policy_lockout_safety_check = null
  custom_key_store_id                = null
  customer_master_key_spec           = "SYMMETRIC_DEFAULT"
  deletion_window_in_days            = null
  description                        = "Default key that protects my Backup data when no other key is defined"
  enable_key_rotation                = true
  is_enabled                         = true
  key_usage                          = "ENCRYPT_DECRYPT"
  multi_region                       = false
  policy = jsonencode({
    Id = "auto-backup-1"
    Statement = [{
      Action = ["kms:CreateGrant", "kms:Decrypt", "kms:GenerateDataKey*"]
      Condition = {
        StringEquals = {
          "kms:CallerAccount" = "141812438321"
          "kms:ViaService"    = "backup.us-east-1.amazonaws.com"
        }
      }
      Effect = "Allow"
      Principal = {
        AWS = "*"
      }
      Resource = "*"
      Sid      = "Allow access through Backup for all principals in the account that are authorized to use Backup Storage"
      }, {
      Action = ["kms:Describe*", "kms:Get*", "kms:List*", "kms:RevokeGrant"]
      Effect = "Allow"
      Principal = {
        AWS = "arn:aws:iam::141812438321:root"
      }
      Resource = "*"
      Sid      = "Allow direct access to key metadata to the account"
    }]
    Version = "2012-10-17"
  })
  region                  = "us-east-1"
  rotation_period_in_days = 365
  tags                    = {}
  tags_all                = {}
  xks_key_id              = null
}

# __generated__ by Terraform from "luther-55f50492-default-tfstate-s3-tbzt@us-east-1"
resource "aws_s3_bucket_public_access_block" "luther_55f50492_default_tfstate_s3_tbzt_public_access_block" {
  block_public_acls       = true
  block_public_policy     = true
  bucket                  = "luther-55f50492-default-tfstate-s3-tbzt"
  ignore_public_acls      = true
  region                  = "us-east-1"
  restrict_public_buckets = true
  skip_destroy            = null
}

# __generated__ by Terraform from "sgr-07ab720dc527b1d67@us-east-1"
resource "aws_vpc_security_group_ingress_rule" "sgr_07ab720dc527b1d67" {
  cidr_ipv4                    = null
  cidr_ipv6                    = null
  description                  = null
  from_port                    = null
  ip_protocol                  = "-1"
  prefix_list_id               = null
  referenced_security_group_id = "sg-0003b998198cbbf54"
  region                       = "us-east-1"
  security_group_id            = "sg-0003b998198cbbf54"
  tags                         = null
  to_port                      = null
}

# __generated__ by Terraform
resource "aws_kms_alias" "alias_aws_rds" {
  name          = "alias/aws/rds"
  region        = "us-east-1"
  target_key_id = "9c83229e-504f-4742-9037-6e7b980a8162"
}

# __generated__ by Terraform from "sgr-0535bc616131addba@us-east-1"
resource "aws_vpc_security_group_egress_rule" "sgr_0535bc616131addba" {
  cidr_ipv4                    = "0.0.0.0/0"
  cidr_ipv6                    = null
  description                  = null
  from_port                    = null
  ip_protocol                  = "-1"
  prefix_list_id               = null
  referenced_security_group_id = null
  region                       = "us-east-1"
  security_group_id            = "sg-07ab0fd93782eb359"
  tags                         = null
  to_port                      = null
}

# __generated__ by Terraform
resource "aws_lb_target_group" "io_f_v6e_hzw_zt_tg" {
  deregistration_delay               = "300"
  ip_address_type                    = "ipv4"
  lambda_multi_value_headers_enabled = null
  load_balancing_algorithm_type      = "round_robin"
  load_balancing_anomaly_mitigation  = "off"
  load_balancing_cross_zone_enabled  = "use_load_balancer_configuration"
  name                               = "io-f-v6e-hzw-zt-tg"
  port                               = 80
  protocol                           = "HTTP"
  protocol_version                   = "HTTP1"
  proxy_protocol_v2                  = null
  region                             = "us-east-1"
  slow_start                         = 0
  tags = {
    Component    = "insideout"
    Environment  = "prod"
    ID           = "alb0"
    Name         = "io-f-v6e-hzw-zt-prod-luthersystems-insideout-alb-tg"
    Organization = "luthersystems"
    Project      = "io-f-v6e-hzw-zt"
    Resource     = "alb"
    Subcomponent = "alb"
  }
  tags_all = {
    Component    = "insideout"
    Environment  = "prod"
    ID           = "alb0"
    Name         = "io-f-v6e-hzw-zt-prod-luthersystems-insideout-alb-tg"
    Organization = "luthersystems"
    Project      = "io-f-v6e-hzw-zt"
    Resource     = "alb"
    Subcomponent = "alb"
  }
  target_control_port = 0
  target_type         = "instance"
  vpc_id              = "vpc-0328efde06fc443f8"
  health_check {
    enabled             = true
    healthy_threshold   = 2
    interval            = 30
    matcher             = "200-399"
    path                = "/"
    port                = "traffic-port"
    protocol            = "HTTP"
    timeout             = 5
    unhealthy_threshold = 3
  }
  stickiness {
    cookie_duration = 86400
    cookie_name     = null
    enabled         = false
    type            = "lb_cookie"
  }
  target_failover {
    on_deregistration = null
    on_unhealthy      = null
  }
  target_group_health {
    dns_failover {
      minimum_healthy_targets_count      = "1"
      minimum_healthy_targets_percentage = "off"
    }
    unhealthy_state_routing {
      minimum_healthy_targets_count      = 1
      minimum_healthy_targets_percentage = "off"
    }
  }
  target_health_state {
    enable_unhealthy_connection_termination = null
    unhealthy_draining_interval             = null
  }
}

# __generated__ by Terraform from "alias/415161a7-tfstate@us-east-1"
resource "aws_kms_alias" "alias_415161a7_tfstate" {
  name          = "alias/415161a7-tfstate"
  region        = "us-east-1"
  target_key_id = "9aa3e37e-e941-45ce-9be0-6562cfc60525"
}

# __generated__ by Terraform
resource "aws_route_table" "rtb_063fc234ab0b63876" {
  propagating_vgws = []
  region           = "us-east-1"
  route = [{
    carrier_gateway_id         = ""
    cidr_block                 = "0.0.0.0/0"
    core_network_arn           = ""
    destination_prefix_list_id = ""
    egress_only_gateway_id     = ""
    gateway_id                 = "igw-0edaf6f0910e380be"
    ipv6_cidr_block            = ""
    local_gateway_id           = ""
    nat_gateway_id             = ""
    network_interface_id       = ""
    transit_gateway_id         = ""
    vpc_endpoint_id            = ""
    vpc_peering_connection_id  = ""
  }]
  tags = {
    Component    = "insideout"
    Environment  = "prod"
    ID           = "vpc0"
    Name         = "io-f-v6e-hzw-zt-prod-luthersystems-insideout-vpc-vpc0"
    Organization = "luthersystems"
    Project      = "io-f-v6e-hzw-zt"
    Resource     = "vpc"
    Subcomponent = "vpc"
  }
  tags_all = {
    Component    = "insideout"
    Environment  = "prod"
    ID           = "vpc0"
    Name         = "io-f-v6e-hzw-zt-prod-luthersystems-insideout-vpc-vpc0"
    Organization = "luthersystems"
    Project      = "io-f-v6e-hzw-zt"
    Resource     = "vpc"
    Subcomponent = "vpc"
  }
  vpc_id = "vpc-0328efde06fc443f8"
}

# __generated__ by Terraform
resource "aws_subnet" "subnet_0d07c5585d535d5b5" {
  assign_ipv6_address_on_creation                = false
  availability_zone                              = "us-east-1b"
  availability_zone_id                           = "use1-az2"
  cidr_block                                     = "172.31.80.0/20"
  customer_owned_ipv4_pool                       = null
  enable_dns64                                   = false
  enable_lni_at_device_index                     = 0
  enable_resource_name_dns_a_record_on_launch    = false
  enable_resource_name_dns_aaaa_record_on_launch = false
  ipv4_ipam_pool_id                              = null
  ipv4_netmask_length                            = null
  ipv6_ipam_pool_id                              = null
  ipv6_native                                    = false
  ipv6_netmask_length                            = null
  map_customer_owned_ip_on_launch                = false
  map_public_ip_on_launch                        = true
  outpost_arn                                    = null
  private_dns_hostname_type_on_launch            = "ip-name"
  region                                         = "us-east-1"
  tags                                           = {}
  tags_all                                       = {}
  vpc_id                                         = "vpc-0648c27c4d5455bc7"
}

# __generated__ by Terraform from "4hmoaslnr0@us-east-1"
resource "aws_apigatewayv2_api" "io_f_v6e_hzw_zt_prod_luthersystems_insideout_apigw_apigw0" {
  api_key_selection_expression = "$request.header.x-api-key"
  body                         = null
  credentials_arn              = null
  description                  = null
  disable_execute_api_endpoint = false
  fail_on_warnings             = null
  ip_address_type              = "ipv4"
  name                         = "io-f-v6e-hzw-zt-prod-luthersystems-insideout-apigw-apigw0"
  protocol_type                = "HTTP"
  region                       = "us-east-1"
  route_key                    = null
  route_selection_expression   = "$request.method $request.path"
  tags = {
    Component    = "insideout"
    Environment  = "prod"
    ID           = "apigw0"
    Name         = "io-f-v6e-hzw-zt-prod-luthersystems-insideout-apigw-apigw0"
    Organization = "luthersystems"
    Project      = "io-f-v6e-hzw-zt"
    Resource     = "apigw"
    Subcomponent = "apigw"
  }
  tags_all = {
    Component    = "insideout"
    Environment  = "prod"
    ID           = "apigw0"
    Name         = "io-f-v6e-hzw-zt-prod-luthersystems-insideout-apigw-apigw0"
    Organization = "luthersystems"
    Project      = "io-f-v6e-hzw-zt"
    Resource     = "apigw"
    Subcomponent = "apigw"
  }
  target  = null
  version = null
}

# __generated__ by Terraform from "Z092953819ZWF0GHF5APB"
resource "aws_route53_zone" "r_415161a7_apps_platform_luthersystemsapp_com" {
  comment                     = "Managed by Terraform"
  delegation_set_id           = null
  enable_accelerated_recovery = false
  force_destroy               = null
  name                        = "415161a7.apps.platform.luthersystemsapp.com"
  tags = {
    Component    = "dns"
    Environment  = "default"
    ID           = "0"
    Name         = "415161a7-default-luther-dns-zone-0"
    Organization = "luther"
    Project      = "415161a7"
    Resource     = "zone"
  }
  tags_all = {
    Component    = "dns"
    Environment  = "default"
    ID           = "0"
    Name         = "415161a7-default-luther-dns-zone-0"
    Organization = "luther"
    Project      = "415161a7"
    Resource     = "zone"
  }
}

# __generated__ by Terraform
resource "aws_subnet" "subnet_0e16da351f9a4626a" {
  assign_ipv6_address_on_creation                = false
  availability_zone                              = "us-east-1a"
  availability_zone_id                           = "use1-az1"
  cidr_block                                     = "10.1.0.0/20"
  customer_owned_ipv4_pool                       = null
  enable_dns64                                   = false
  enable_lni_at_device_index                     = 0
  enable_resource_name_dns_a_record_on_launch    = false
  enable_resource_name_dns_aaaa_record_on_launch = false
  ipv4_ipam_pool_id                              = null
  ipv4_netmask_length                            = null
  ipv6_ipam_pool_id                              = null
  ipv6_native                                    = false
  ipv6_netmask_length                            = null
  map_customer_owned_ip_on_launch                = false
  map_public_ip_on_launch                        = false
  outpost_arn                                    = null
  private_dns_hostname_type_on_launch            = "ip-name"
  region                                         = "us-east-1"
  tags = {
    Component                         = "insideout"
    Environment                       = "prod"
    ID                                = "vpc0"
    Name                              = "io-f0ttnkgjdvee-prod-luthersystems-insideout-vpc-vpc0"
    Organization                      = "luthersystems"
    Project                           = "io-f0ttnkgjdvee"
    Resource                          = "vpc"
    Subcomponent                      = "vpc"
    "kubernetes.io/role/internal-elb" = "1"
  }
  tags_all = {
    Component                         = "insideout"
    Environment                       = "prod"
    ID                                = "vpc0"
    Name                              = "io-f0ttnkgjdvee-prod-luthersystems-insideout-vpc-vpc0"
    Organization                      = "luthersystems"
    Project                           = "io-f0ttnkgjdvee"
    Resource                          = "vpc"
    Subcomponent                      = "vpc"
    "kubernetes.io/role/internal-elb" = "1"
  }
  vpc_id = "vpc-0b5b7a95d51782af5"
}

# __generated__ by Terraform from "io-h3a-v495cjgt-prod-luthersystems-insideout-cwm-cwm0@us-east-1"
resource "aws_cloudwatch_dashboard" "io_h3a_v495cjgt_prod_luthersystems_insideout_cwm_cwm0" {
  dashboard_body = jsonencode({
    widgets = [{
      h = 6
      properties = {
        metrics = [["AWS/ApplicationELB", "HTTPCode_Target_5XX_Count", {
          stat = "Sum"
          }], ["AWS/ApplicationELB", "HTTPCode_Target_5XX_Count", "LoadBalancer", "app/io-h3a-v495cjgt-alb/9b73e0c162962e16", {
          stat = "Sum"
          }], [".", "TargetResponseTime", {
          stat = "Average"
          }], ["AWS/ApplicationELB", "TargetResponseTime", "LoadBalancer", "app/io-h3a-v495cjgt-alb/9b73e0c162962e16", {
          stat = "Average"
        }]]
        period = 300
        region = "us-east-1"
        stat   = "Sum"
        title  = "ALB 5XX & Latency"
      }
      type = "metric"
      w    = 24
      x    = 0
      y    = 24
    }]
  })
  dashboard_name = "io-h3a-v495cjgt-prod-luthersystems-insideout-cwm-cwm0"
  region         = "us-east-1"
}

# __generated__ by Terraform from "/aws/rds/instance/io-kv8abqujejtk-prod-luthersystems-insideout-rds-rds0-replica-1/postgresql@us-east-1"
resource "aws_cloudwatch_log_group" "aws_rds_instance_io_kv8abqujejtk_prod_luthersystems_insideout_rds_rds0_replica_1_pos" {
  deletion_protection_enabled = false
  kms_key_id                  = null
  log_group_class             = "STANDARD"
  name                        = "/aws/rds/instance/io-kv8abqujejtk-prod-luthersystems-insideout-rds-rds0-replica-1/postgresql"
  region                      = "us-east-1"
  retention_in_days           = 0
  skip_destroy                = false
  tags                        = {}
  tags_all                    = {}
}

# __generated__ by Terraform
resource "aws_kms_alias" "alias_aws_acm" {
  name          = "alias/aws/acm"
  region        = "us-east-1"
  target_key_id = "6a650196-62dc-4fdf-8be2-16ed5e1e66d4"
}

# __generated__ by Terraform
resource "aws_vpc" "vpc_0648c27c4d5455bc7" {
  assign_generated_ipv6_cidr_block     = false
  cidr_block                           = "172.31.0.0/16"
  enable_dns_hostnames                 = true
  enable_dns_support                   = true
  enable_network_address_usage_metrics = false
  instance_tenancy                     = "default"
  ipv4_ipam_pool_id                    = null
  ipv4_netmask_length                  = null
  ipv6_ipam_pool_id                    = null
  ipv6_netmask_length                  = 0
  region                               = "us-east-1"
  tags                                 = {}
  tags_all                             = {}
}

# __generated__ by Terraform from "Z09153683A4ZGXVF79ZKL"
resource "aws_route53_zone" "r_55f50492_apps_platform_luthersystemsapp_com" {
  comment                     = "Managed by Terraform"
  delegation_set_id           = null
  enable_accelerated_recovery = false
  force_destroy               = null
  name                        = "55f50492.apps.platform.luthersystemsapp.com"
  tags = {
    Component    = "dns"
    Environment  = "default"
    ID           = "0"
    Name         = "55f50492-default-luther-dns-zone-0"
    Organization = "luther"
    Project      = "55f50492"
    Resource     = "zone"
  }
  tags_all = {
    Component    = "dns"
    Environment  = "default"
    ID           = "0"
    Name         = "55f50492-default-luther-dns-zone-0"
    Organization = "luther"
    Project      = "55f50492"
    Resource     = "zone"
  }
}

# __generated__ by Terraform from "sgr-0cfd55309a95be11e@us-east-1"
resource "aws_vpc_security_group_egress_rule" "sgr_0cfd55309a95be11e" {
  cidr_ipv4                    = "0.0.0.0/0"
  cidr_ipv6                    = null
  description                  = null
  from_port                    = null
  ip_protocol                  = "-1"
  prefix_list_id               = null
  referenced_security_group_id = null
  region                       = "us-east-1"
  security_group_id            = "sg-05b33367d0263c42d"
  tags                         = null
  to_port                      = null
}

# __generated__ by Terraform
resource "aws_route_table" "rtb_048b1afa1f0cf9c2c" {
  propagating_vgws = []
  region           = "us-east-1"
  route = [{
    carrier_gateway_id         = ""
    cidr_block                 = "0.0.0.0/0"
    core_network_arn           = ""
    destination_prefix_list_id = ""
    egress_only_gateway_id     = ""
    gateway_id                 = ""
    ipv6_cidr_block            = ""
    local_gateway_id           = ""
    nat_gateway_id             = "nat-08bcfea9265221ec4"
    network_interface_id       = ""
    transit_gateway_id         = ""
    vpc_endpoint_id            = ""
    vpc_peering_connection_id  = ""
  }]
  tags = {
    Component    = "insideout"
    Environment  = "prod"
    ID           = "vpc0"
    Name         = "io-f0ttnkgjdvee-prod-luthersystems-insideout-vpc-vpc0"
    Organization = "luthersystems"
    Project      = "io-f0ttnkgjdvee"
    Resource     = "vpc"
    Subcomponent = "vpc"
  }
  tags_all = {
    Component    = "insideout"
    Environment  = "prod"
    ID           = "vpc0"
    Name         = "io-f0ttnkgjdvee-prod-luthersystems-insideout-vpc-vpc0"
    Organization = "luthersystems"
    Project      = "io-f0ttnkgjdvee"
    Resource     = "vpc"
    Subcomponent = "vpc"
  }
  vpc_id = "vpc-0b5b7a95d51782af5"
}

# __generated__ by Terraform from "luther-55f50492-default-tfstate-s3-tbzt@us-east-1"
resource "aws_s3_bucket_versioning" "luther_55f50492_default_tfstate_s3_tbzt_versioning" {
  bucket = "luther-55f50492-default-tfstate-s3-tbzt"
  mfa    = null
  region = "us-east-1"
  versioning_configuration {
    status = "Enabled"
  }
}

# __generated__ by Terraform from "sg-00e0be0db6405773e@us-east-1"
resource "aws_security_group" "default" {
  description            = "default VPC security group"
  egress                 = []
  ingress                = []
  name                   = "default"
  region                 = "us-east-1"
  revoke_rules_on_delete = null
  tags = {
    Component    = "insideout"
    Environment  = "prod"
    ID           = "vpc0"
    Name         = "io-f0ttnkgjdvee-prod-luthersystems-insideout-vpc-vpc0"
    Organization = "luthersystems"
    Project      = "io-f0ttnkgjdvee"
    Resource     = "vpc"
    Subcomponent = "vpc"
  }
  tags_all = {
    Component    = "insideout"
    Environment  = "prod"
    ID           = "vpc0"
    Name         = "io-f0ttnkgjdvee-prod-luthersystems-insideout-vpc-vpc0"
    Organization = "luthersystems"
    Project      = "io-f0ttnkgjdvee"
    Resource     = "vpc"
    Subcomponent = "vpc"
  }
  vpc_id = "vpc-0b5b7a95d51782af5"
}

# __generated__ by Terraform
resource "aws_network_interface" "eni_0d62bb4aa2988e052" {
  description               = "ELB app/io-f-v6e-hzw-zt-alb/bcda3f52ff22fa50"
  interface_type            = "interface"
  ipv4_prefix_count         = 0
  ipv4_prefixes             = []
  ipv6_address_count        = 0
  ipv6_address_list         = []
  ipv6_address_list_enabled = null
  ipv6_addresses            = []
  ipv6_prefix_count         = 0
  ipv6_prefixes             = []
  private_ip                = "10.1.128.22"
  private_ip_list           = ["10.1.128.22"]
  private_ip_list_enabled   = null
  private_ips               = ["10.1.128.22"]
  private_ips_count         = 0
  region                    = "us-east-1"
  security_groups           = ["sg-05b33367d0263c42d"]
  source_dest_check         = true
  subnet_id                 = "subnet-08b4ceeaf7cbeccdb"
  tags                      = {}
  tags_all                  = {}
  attachment {
    device_index       = 1
    instance           = ""
    network_card_index = 0
  }
}

# __generated__ by Terraform from "/aws/containerinsights/io-hrbs5zprbk51-prod-lu-eks0/host@us-east-1"
resource "aws_cloudwatch_log_group" "aws_containerinsights_io_hrbs5zprbk51_prod_lu_eks0_host" {
  deletion_protection_enabled = false
  kms_key_id                  = null
  log_group_class             = "STANDARD"
  name                        = "/aws/containerinsights/io-hrbs5zprbk51-prod-lu-eks0/host"
  region                      = "us-east-1"
  retention_in_days           = 0
  skip_destroy                = false
  tags                        = {}
  tags_all                    = {}
}

# __generated__ by Terraform
resource "aws_lambda_function" "io_f_v6e_hzw_zt_prod_luthersystems_insideout_lambda_lambdaedf3" {
  architectures                      = ["x86_64"]
  code_sha256                        = "ZrrDlridHRa8Guwdfs+uqIADsdnznvaagDHQwaEjbJk="
  code_signing_config_arn            = null
  description                        = null
  filename                           = null
  function_name                      = "io-f-v6e-hzw-zt-prod-luthersystems-insideout-lambda-lambdaedf3"
  handler                            = "index.handler"
  image_uri                          = null
  kms_key_arn                        = null
  layers                             = []
  memory_size                        = 128
  package_type                       = "Zip"
  publish                            = null
  publish_to                         = null
  region                             = "us-east-1"
  replace_security_groups_on_destroy = null
  replacement_security_group_ids     = null
  reserved_concurrent_executions     = -1
  role                               = "arn:aws:iam::141812438321:role/io-f-v6e-hzw-zt-lambda-exec"
  runtime                            = "nodejs20.x"
  s3_bucket                          = null
  s3_key                             = null
  s3_object_version                  = null
  skip_destroy                       = false
  source_kms_key_arn                 = null
  tags = {
    Component    = "insideout"
    Environment  = "prod"
    ID           = "lambdaedf3"
    Name         = "io-f-v6e-hzw-zt-prod-luthersystems-insideout-lambda-lambdaedf3"
    Organization = "luthersystems"
    Project      = "io-f-v6e-hzw-zt"
    Resource     = "lambda"
    Subcomponent = "lambda"
  }
  tags_all = {
    Component    = "insideout"
    Environment  = "prod"
    ID           = "lambdaedf3"
    Name         = "io-f-v6e-hzw-zt-prod-luthersystems-insideout-lambda-lambdaedf3"
    Organization = "luthersystems"
    Project      = "io-f-v6e-hzw-zt"
    Resource     = "lambda"
    Subcomponent = "lambda"
  }
  timeout = 3
  ephemeral_storage {
    size = 512
  }
  logging_config {
    application_log_level = null
    log_format            = "Text"
    log_group             = "/aws/lambda/io-f-v6e-hzw-zt-prod-luthersystems-insideout-lambda-lambdaedf3"
    system_log_level      = null
  }
  tracing_config {
    mode = "PassThrough"
  }
}

# __generated__ by Terraform from "/aws/containerinsights/io-hrbs5zprbk51-prod-lu-eks0/dataplane@us-east-1"
resource "aws_cloudwatch_log_group" "aws_containerinsights_io_hrbs5zprbk51_prod_lu_eks0_dataplane" {
  deletion_protection_enabled = false
  kms_key_id                  = null
  log_group_class             = "STANDARD"
  name                        = "/aws/containerinsights/io-hrbs5zprbk51-prod-lu-eks0/dataplane"
  region                      = "us-east-1"
  retention_in_days           = 0
  skip_destroy                = false
  tags                        = {}
  tags_all                    = {}
}

# __generated__ by Terraform from "/aws/rds/instance/io-hrbs5zprbk51-prod-luthersystems-insideout-rds-rds0/postgresql@us-east-1"
resource "aws_cloudwatch_log_group" "aws_rds_instance_io_hrbs5zprbk51_prod_luthersystems_insideout_rds_rds0_postgresql" {
  deletion_protection_enabled = false
  kms_key_id                  = null
  log_group_class             = "STANDARD"
  name                        = "/aws/rds/instance/io-hrbs5zprbk51-prod-luthersystems-insideout-rds-rds0/postgresql"
  region                      = "us-east-1"
  retention_in_days           = 0
  skip_destroy                = false
  tags                        = {}
  tags_all                    = {}
}

# __generated__ by Terraform
resource "aws_subnet" "subnet_002d7836ce590c161" {
  assign_ipv6_address_on_creation                = false
  availability_zone                              = "us-east-1b"
  availability_zone_id                           = "use1-az2"
  cidr_block                                     = "10.1.144.0/20"
  customer_owned_ipv4_pool                       = null
  enable_dns64                                   = false
  enable_lni_at_device_index                     = 0
  enable_resource_name_dns_a_record_on_launch    = false
  enable_resource_name_dns_aaaa_record_on_launch = false
  ipv4_ipam_pool_id                              = null
  ipv4_netmask_length                            = null
  ipv6_ipam_pool_id                              = null
  ipv6_native                                    = false
  ipv6_netmask_length                            = null
  map_customer_owned_ip_on_launch                = false
  map_public_ip_on_launch                        = true
  outpost_arn                                    = null
  private_dns_hostname_type_on_launch            = "ip-name"
  region                                         = "us-east-1"
  tags = {
    Component                = "insideout"
    Environment              = "prod"
    ID                       = "vpc0"
    Name                     = "io-f0ttnkgjdvee-prod-luthersystems-insideout-vpc-vpc0"
    Organization             = "luthersystems"
    Project                  = "io-f0ttnkgjdvee"
    Resource                 = "vpc"
    Subcomponent             = "vpc"
    "kubernetes.io/role/elb" = "1"
  }
  tags_all = {
    Component                = "insideout"
    Environment              = "prod"
    ID                       = "vpc0"
    Name                     = "io-f0ttnkgjdvee-prod-luthersystems-insideout-vpc-vpc0"
    Organization             = "luthersystems"
    Project                  = "io-f0ttnkgjdvee"
    Resource                 = "vpc"
    Subcomponent             = "vpc"
    "kubernetes.io/role/elb" = "1"
  }
  vpc_id = "vpc-0b5b7a95d51782af5"
}

# __generated__ by Terraform
resource "aws_subnet" "subnet_0b5999dbeddd2546c" {
  assign_ipv6_address_on_creation                = false
  availability_zone                              = "us-east-1a"
  availability_zone_id                           = "use1-az1"
  cidr_block                                     = "172.31.0.0/20"
  customer_owned_ipv4_pool                       = null
  enable_dns64                                   = false
  enable_lni_at_device_index                     = 0
  enable_resource_name_dns_a_record_on_launch    = false
  enable_resource_name_dns_aaaa_record_on_launch = false
  ipv4_ipam_pool_id                              = null
  ipv4_netmask_length                            = null
  ipv6_ipam_pool_id                              = null
  ipv6_native                                    = false
  ipv6_netmask_length                            = null
  map_customer_owned_ip_on_launch                = false
  map_public_ip_on_launch                        = true
  outpost_arn                                    = null
  private_dns_hostname_type_on_launch            = "ip-name"
  region                                         = "us-east-1"
  tags                                           = {}
  tags_all                                       = {}
  vpc_id                                         = "vpc-0648c27c4d5455bc7"
}

# __generated__ by Terraform
resource "aws_vpc" "vpc_0b5b7a95d51782af5" {
  assign_generated_ipv6_cidr_block     = false
  cidr_block                           = "10.1.0.0/16"
  enable_dns_hostnames                 = true
  enable_dns_support                   = true
  enable_network_address_usage_metrics = false
  instance_tenancy                     = "default"
  ipv4_ipam_pool_id                    = null
  ipv4_netmask_length                  = null
  ipv6_ipam_pool_id                    = null
  ipv6_netmask_length                  = 0
  region                               = "us-east-1"
  tags = {
    Component    = "insideout"
    Environment  = "prod"
    ID           = "vpc0"
    Name         = "io-f0ttnkgjdvee-prod-luthersystems-insideout-vpc-vpc0"
    Organization = "luthersystems"
    Project      = "io-f0ttnkgjdvee"
    Resource     = "vpc"
    Subcomponent = "vpc"
  }
  tags_all = {
    Component    = "insideout"
    Environment  = "prod"
    ID           = "vpc0"
    Name         = "io-f0ttnkgjdvee-prod-luthersystems-insideout-vpc-vpc0"
    Organization = "luthersystems"
    Project      = "io-f0ttnkgjdvee"
    Resource     = "vpc"
    Subcomponent = "vpc"
  }
}

# __generated__ by Terraform
resource "aws_kms_alias" "alias_aws_ebs" {
  name          = "alias/aws/ebs"
  region        = "us-east-1"
  target_key_id = "cb27d9ef-4b39-4604-978c-5160e4a0427b"
}

# __generated__ by Terraform from "cb27d9ef-4b39-4604-978c-5160e4a0427b@us-east-1"
resource "aws_kms_key" "cb27d9ef_4b39_4604_978c_5160e4a0427b" {
  bypass_policy_lockout_safety_check = null
  custom_key_store_id                = null
  customer_master_key_spec           = "SYMMETRIC_DEFAULT"
  deletion_window_in_days            = null
  description                        = "Default key that protects my EBS volumes when no other key is defined"
  enable_key_rotation                = true
  is_enabled                         = true
  key_usage                          = "ENCRYPT_DECRYPT"
  multi_region                       = false
  policy = jsonencode({
    Id = "auto-ebs-2"
    Statement = [{
      Action = ["kms:Encrypt", "kms:Decrypt", "kms:ReEncrypt*", "kms:GenerateDataKey*", "kms:CreateGrant", "kms:DescribeKey"]
      Condition = {
        StringEquals = {
          "kms:CallerAccount" = "141812438321"
          "kms:ViaService"    = "ec2.us-east-1.amazonaws.com"
        }
      }
      Effect = "Allow"
      Principal = {
        AWS = "*"
      }
      Resource = "*"
      Sid      = "Allow access through EBS for all principals in the account that are authorized to use EBS"
      }, {
      Action = ["kms:Describe*", "kms:Get*", "kms:List*", "kms:RevokeGrant"]
      Effect = "Allow"
      Principal = {
        AWS = "arn:aws:iam::141812438321:root"
      }
      Resource = "*"
      Sid      = "Allow direct access to key metadata to the account"
    }]
    Version = "2012-10-17"
  })
  region                  = "us-east-1"
  rotation_period_in_days = 365
  tags                    = {}
  tags_all                = {}
  xks_key_id              = null
}

# __generated__ by Terraform from "alias/io-f-v6e-hzw-zt-prod-luthersystems-insideout-kms-kmsca5a@us-east-1"
resource "aws_kms_alias" "alias_io_f_v6e_hzw_zt_prod_luthersystems_insideout_kms_kmsca5a" {
  name          = "alias/io-f-v6e-hzw-zt-prod-luthersystems-insideout-kms-kmsca5a"
  region        = "us-east-1"
  target_key_id = "74a45617-3c06-4ba6-b99c-ec43ff48e862"
}

# __generated__ by Terraform from "dopt-0bfb5b8e3dd556b7b@us-east-1"
resource "aws_vpc_dhcp_options" "dopt_0bfb5b8e3dd556b7b" {
  domain_name                       = "ec2.internal"
  domain_name_servers               = ["AmazonProvidedDNS"]
  ipv6_address_preferred_lease_time = null
  netbios_name_servers              = []
  netbios_node_type                 = null
  ntp_servers                       = []
  region                            = "us-east-1"
  tags                              = {}
  tags_all                          = {}
}

# __generated__ by Terraform from "sgr-0870321c70e97f8bd@us-east-1"
resource "aws_vpc_security_group_ingress_rule" "sgr_0870321c70e97f8bd" {
  cidr_ipv4                    = "0.0.0.0/0"
  cidr_ipv6                    = null
  description                  = "Allow HTTP"
  from_port                    = 80
  ip_protocol                  = "tcp"
  prefix_list_id               = null
  referenced_security_group_id = null
  region                       = "us-east-1"
  security_group_id            = "sg-05b33367d0263c42d"
  tags                         = null
  to_port                      = 80
}

# __generated__ by Terraform from "/aws/rds/instance/io-w1bgiwv6ufyx-prod-luthersystems-insideout-rds-rds0/postgresql@us-east-1"
resource "aws_cloudwatch_log_group" "aws_rds_instance_io_w1bgiwv6ufyx_prod_luthersystems_insideout_rds_rds0_postgresql" {
  deletion_protection_enabled = false
  kms_key_id                  = null
  log_group_class             = "STANDARD"
  name                        = "/aws/rds/instance/io-w1bgiwv6ufyx-prod-luthersystems-insideout-rds-rds0/postgresql"
  region                      = "us-east-1"
  retention_in_days           = 0
  skip_destroy                = false
  tags                        = {}
  tags_all                    = {}
}

# __generated__ by Terraform from "alias/d7d00f09-tfstate@us-east-1"
resource "aws_kms_alias" "alias_d7d00f09_tfstate" {
  name          = "alias/d7d00f09-tfstate"
  region        = "us-east-1"
  target_key_id = "9061c152-2eab-4c4c-b23c-aefda89d0592"
}

# __generated__ by Terraform from "4cbb8c7d-bf43-4d62-aea0-91e336b364ce@us-east-1"
resource "aws_kms_key" "r_4cbb8c7d_bf43_4d62_aea0_91e336b364ce" {
  bypass_policy_lockout_safety_check = null
  custom_key_store_id                = null
  customer_master_key_spec           = "SYMMETRIC_DEFAULT"
  deletion_window_in_days            = null
  description                        = "Default key that protects my DynamoDB data when no other key is defined"
  enable_key_rotation                = true
  is_enabled                         = true
  key_usage                          = "ENCRYPT_DECRYPT"
  multi_region                       = false
  policy = jsonencode({
    Id = "auto-dynamodb-3"
    Statement = [{
      Action = ["kms:Encrypt", "kms:Decrypt", "kms:ReEncrypt*", "kms:GenerateDataKey*", "kms:CreateGrant", "kms:DescribeKey"]
      Condition = {
        StringEquals = {
          "kms:CallerAccount" = "141812438321"
        }
        StringLike = {
          "kms:ViaService" = "dynamodb.*.amazonaws.com"
        }
      }
      Effect = "Allow"
      Principal = {
        AWS = "*"
      }
      Resource = "*"
      Sid      = "Allow access through Amazon DynamoDB for all principals in the account that are authorized to use Amazon DynamoDB"
      }, {
      Action = ["kms:Describe*", "kms:Get*", "kms:List*", "kms:RevokeGrant"]
      Effect = "Allow"
      Principal = {
        AWS = "arn:aws:iam::141812438321:root"
      }
      Resource = "*"
      Sid      = "Allow direct access to key metadata to the account"
      }, {
      Action = ["kms:Describe*", "kms:Get*", "kms:List*"]
      Effect = "Allow"
      Principal = {
        Service = "dynamodb.amazonaws.com"
      }
      Resource = "*"
      Sid      = "Allow DynamoDB to directly describe the key"
    }]
    Version = "2012-10-17"
  })
  region                  = "us-east-1"
  rotation_period_in_days = 365
  tags                    = {}
  tags_all                = {}
  xks_key_id              = null
}

# __generated__ by Terraform from "luther-55f50492-default-tfstate-s3-tbzt@us-east-1"
resource "aws_s3_bucket_ownership_controls" "luther_55f50492_default_tfstate_s3_tbzt_ownership" {
  bucket = "luther-55f50492-default-tfstate-s3-tbzt"
  region = "us-east-1"
  rule {
    object_ownership = "BucketOwnerEnforced"
  }
}

# __generated__ by Terraform from "RDSOSMetrics@us-east-1"
resource "aws_cloudwatch_log_group" "rdsosmetrics" {
  deletion_protection_enabled = false
  kms_key_id                  = null
  log_group_class             = "STANDARD"
  name                        = "RDSOSMetrics"
  region                      = "us-east-1"
  retention_in_days           = 30
  skip_destroy                = false
  tags                        = {}
  tags_all                    = {}
}

# __generated__ by Terraform from "/aws/lambda/io-f-v6e-hzw-zt-prod-luthersystems-insideout-lambda-lambdaedf3@us-east-1"
resource "aws_cloudwatch_log_group" "aws_lambda_io_f_v6e_hzw_zt_prod_luthersystems_insideout_lambda_lambdaedf3" {
  deletion_protection_enabled = false
  kms_key_id                  = null
  log_group_class             = "STANDARD"
  name                        = "/aws/lambda/io-f-v6e-hzw-zt-prod-luthersystems-insideout-lambda-lambdaedf3"
  region                      = "us-east-1"
  retention_in_days           = 14
  skip_destroy                = false
  tags = {
    Component    = "insideout"
    Environment  = "prod"
    ID           = "lambdaedf3"
    Name         = "io-f-v6e-hzw-zt-prod-luthersystems-insideout-lambda-lambdaedf3"
    Organization = "luthersystems"
    Project      = "io-f-v6e-hzw-zt"
    Resource     = "lambda"
    Subcomponent = "lambda"
  }
  tags_all = {
    Component    = "insideout"
    Environment  = "prod"
    ID           = "lambdaedf3"
    Name         = "io-f-v6e-hzw-zt-prod-luthersystems-insideout-lambda-lambdaedf3"
    Organization = "luthersystems"
    Project      = "io-f-v6e-hzw-zt"
    Resource     = "lambda"
    Subcomponent = "lambda"
  }
}

# __generated__ by Terraform from "/aws/rds/instance/io-ofamkmdb7-ge-prod-luthersystems-insideout-rds-rds0/postgresql@us-east-1"
resource "aws_cloudwatch_log_group" "aws_rds_instance_io_ofamkmdb7_ge_prod_luthersystems_insideout_rds_rds0_postgresql" {
  deletion_protection_enabled = false
  kms_key_id                  = null
  log_group_class             = "STANDARD"
  name                        = "/aws/rds/instance/io-ofamkmdb7-ge-prod-luthersystems-insideout-rds-rds0/postgresql"
  region                      = "us-east-1"
  retention_in_days           = 0
  skip_destroy                = false
  tags                        = {}
  tags_all                    = {}
}

# __generated__ by Terraform
resource "aws_subnet" "subnet_0a9e275a33c1d279f" {
  assign_ipv6_address_on_creation                = false
  availability_zone                              = "us-east-1b"
  availability_zone_id                           = "use1-az2"
  cidr_block                                     = "10.1.144.0/20"
  customer_owned_ipv4_pool                       = null
  enable_dns64                                   = false
  enable_lni_at_device_index                     = 0
  enable_resource_name_dns_a_record_on_launch    = false
  enable_resource_name_dns_aaaa_record_on_launch = false
  ipv4_ipam_pool_id                              = null
  ipv4_netmask_length                            = null
  ipv6_ipam_pool_id                              = null
  ipv6_native                                    = false
  ipv6_netmask_length                            = null
  map_customer_owned_ip_on_launch                = false
  map_public_ip_on_launch                        = true
  outpost_arn                                    = null
  private_dns_hostname_type_on_launch            = "ip-name"
  region                                         = "us-east-1"
  tags = {
    Component                = "insideout"
    Environment              = "prod"
    ID                       = "vpc0"
    Name                     = "io-f-v6e-hzw-zt-prod-luthersystems-insideout-vpc-vpc0"
    Organization             = "luthersystems"
    Project                  = "io-f-v6e-hzw-zt"
    Resource                 = "vpc"
    Subcomponent             = "vpc"
    "kubernetes.io/role/elb" = "1"
  }
  tags_all = {
    Component                = "insideout"
    Environment              = "prod"
    ID                       = "vpc0"
    Name                     = "io-f-v6e-hzw-zt-prod-luthersystems-insideout-vpc-vpc0"
    Organization             = "luthersystems"
    Project                  = "io-f-v6e-hzw-zt"
    Resource                 = "vpc"
    Subcomponent             = "vpc"
    "kubernetes.io/role/elb" = "1"
  }
  vpc_id = "vpc-0328efde06fc443f8"
}

# __generated__ by Terraform from "igw-0ac85034e2ee83ea0@us-east-1"
resource "aws_internet_gateway" "igw_0ac85034e2ee83ea0" {
  region   = "us-east-1"
  tags     = {}
  tags_all = {}
  vpc_id   = "vpc-0648c27c4d5455bc7"
}

# __generated__ by Terraform from "igw-0edaf6f0910e380be@us-east-1"
resource "aws_internet_gateway" "igw_0edaf6f0910e380be" {
  region = "us-east-1"
  tags = {
    Component    = "insideout"
    Environment  = "prod"
    ID           = "vpc0"
    Name         = "io-f-v6e-hzw-zt-prod-luthersystems-insideout-vpc-vpc0"
    Organization = "luthersystems"
    Project      = "io-f-v6e-hzw-zt"
    Resource     = "vpc"
    Subcomponent = "vpc"
  }
  tags_all = {
    Component    = "insideout"
    Environment  = "prod"
    ID           = "vpc0"
    Name         = "io-f-v6e-hzw-zt-prod-luthersystems-insideout-vpc-vpc0"
    Organization = "luthersystems"
    Project      = "io-f-v6e-hzw-zt"
    Resource     = "vpc"
    Subcomponent = "vpc"
  }
  vpc_id = "vpc-0328efde06fc443f8"
}

# __generated__ by Terraform from "sg-05b33367d0263c42d@us-east-1"
resource "aws_security_group" "io_f_v6e_hzw_zt_prod_luthersystems_insideout_alb_alb0_sg" {
  description = "Security group for Application Load Balancer"
  egress = [{
    cidr_blocks      = ["0.0.0.0/0"]
    description      = ""
    from_port        = 0
    ipv6_cidr_blocks = []
    prefix_list_ids  = []
    protocol         = "-1"
    security_groups  = []
    self             = false
    to_port          = 0
  }]
  ingress = [{
    cidr_blocks      = ["0.0.0.0/0"]
    description      = "Allow HTTP"
    from_port        = 80
    ipv6_cidr_blocks = []
    prefix_list_ids  = []
    protocol         = "tcp"
    security_groups  = []
    self             = false
    to_port          = 80
    }, {
    cidr_blocks      = ["0.0.0.0/0"]
    description      = "Allow HTTPS"
    from_port        = 443
    ipv6_cidr_blocks = []
    prefix_list_ids  = []
    protocol         = "tcp"
    security_groups  = []
    self             = false
    to_port          = 443
  }]
  name                   = "io-f-v6e-hzw-zt-prod-luthersystems-insideout-alb-alb0-sg"
  region                 = "us-east-1"
  revoke_rules_on_delete = null
  tags = {
    Component    = "insideout"
    Environment  = "prod"
    ID           = "alb0"
    Name         = "io-f-v6e-hzw-zt-prod-luthersystems-insideout-alb-sg"
    Organization = "luthersystems"
    Project      = "io-f-v6e-hzw-zt"
    Resource     = "alb"
    Subcomponent = "alb"
  }
  tags_all = {
    Component    = "insideout"
    Environment  = "prod"
    ID           = "alb0"
    Name         = "io-f-v6e-hzw-zt-prod-luthersystems-insideout-alb-sg"
    Organization = "luthersystems"
    Project      = "io-f-v6e-hzw-zt"
    Resource     = "alb"
    Subcomponent = "alb"
  }
  vpc_id = "vpc-0328efde06fc443f8"
}

# __generated__ by Terraform from "/aws/rds/instance/io-kv8abqujejtk-prod-luthersystems-insideout-rds-rds0/postgresql@us-east-1"
resource "aws_cloudwatch_log_group" "aws_rds_instance_io_kv8abqujejtk_prod_luthersystems_insideout_rds_rds0_postgresql" {
  deletion_protection_enabled = false
  kms_key_id                  = null
  log_group_class             = "STANDARD"
  name                        = "/aws/rds/instance/io-kv8abqujejtk-prod-luthersystems-insideout-rds-rds0/postgresql"
  region                      = "us-east-1"
  retention_in_days           = 0
  skip_destroy                = false
  tags                        = {}
  tags_all                    = {}
}

# __generated__ by Terraform
resource "aws_network_interface" "eni_0ce4fc160b647c275" {
  description               = "Interface for NAT Gateway nat-08bcfea9265221ec4"
  interface_type            = "nat_gateway"
  ipv4_prefix_count         = 0
  ipv4_prefixes             = []
  ipv6_address_count        = 0
  ipv6_address_list         = []
  ipv6_address_list_enabled = null
  ipv6_addresses            = []
  ipv6_prefix_count         = 0
  ipv6_prefixes             = []
  private_ip                = "10.1.143.246"
  private_ip_list           = ["10.1.143.246"]
  private_ip_list_enabled   = null
  private_ips               = ["10.1.143.246"]
  private_ips_count         = 0
  region                    = "us-east-1"
  security_groups           = []
  source_dest_check         = false
  subnet_id                 = "subnet-0e866663c4c5ad4d9"
  tags                      = {}
  tags_all                  = {}
  attachment {
    device_index       = 1
    instance           = ""
    network_card_index = 0
  }
}

# __generated__ by Terraform
resource "aws_subnet" "subnet_0e866663c4c5ad4d9" {
  assign_ipv6_address_on_creation                = false
  availability_zone                              = "us-east-1a"
  availability_zone_id                           = "use1-az1"
  cidr_block                                     = "10.1.128.0/20"
  customer_owned_ipv4_pool                       = null
  enable_dns64                                   = false
  enable_lni_at_device_index                     = 0
  enable_resource_name_dns_a_record_on_launch    = false
  enable_resource_name_dns_aaaa_record_on_launch = false
  ipv4_ipam_pool_id                              = null
  ipv4_netmask_length                            = null
  ipv6_ipam_pool_id                              = null
  ipv6_native                                    = false
  ipv6_netmask_length                            = null
  map_customer_owned_ip_on_launch                = false
  map_public_ip_on_launch                        = true
  outpost_arn                                    = null
  private_dns_hostname_type_on_launch            = "ip-name"
  region                                         = "us-east-1"
  tags = {
    Component                = "insideout"
    Environment              = "prod"
    ID                       = "vpc0"
    Name                     = "io-f0ttnkgjdvee-prod-luthersystems-insideout-vpc-vpc0"
    Organization             = "luthersystems"
    Project                  = "io-f0ttnkgjdvee"
    Resource                 = "vpc"
    Subcomponent             = "vpc"
    "kubernetes.io/role/elb" = "1"
  }
  tags_all = {
    Component                = "insideout"
    Environment              = "prod"
    ID                       = "vpc0"
    Name                     = "io-f0ttnkgjdvee-prod-luthersystems-insideout-vpc-vpc0"
    Organization             = "luthersystems"
    Project                  = "io-f0ttnkgjdvee"
    Resource                 = "vpc"
    Subcomponent             = "vpc"
    "kubernetes.io/role/elb" = "1"
  }
  vpc_id = "vpc-0b5b7a95d51782af5"
}

# __generated__ by Terraform
resource "aws_subnet" "subnet_0ad55f9b023ce6df1" {
  assign_ipv6_address_on_creation                = false
  availability_zone                              = "us-east-1d"
  availability_zone_id                           = "use1-az6"
  cidr_block                                     = "172.31.32.0/20"
  customer_owned_ipv4_pool                       = null
  enable_dns64                                   = false
  enable_lni_at_device_index                     = 0
  enable_resource_name_dns_a_record_on_launch    = false
  enable_resource_name_dns_aaaa_record_on_launch = false
  ipv4_ipam_pool_id                              = null
  ipv4_netmask_length                            = null
  ipv6_ipam_pool_id                              = null
  ipv6_native                                    = false
  ipv6_netmask_length                            = null
  map_customer_owned_ip_on_launch                = false
  map_public_ip_on_launch                        = true
  outpost_arn                                    = null
  private_dns_hostname_type_on_launch            = "ip-name"
  region                                         = "us-east-1"
  tags                                           = {}
  tags_all                                       = {}
  vpc_id                                         = "vpc-0648c27c4d5455bc7"
}

# __generated__ by Terraform
resource "aws_kms_key" "r_9aa3e37e_e941_45ce_9be0_6562cfc60525" {
  bypass_policy_lockout_safety_check = null
  custom_key_store_id                = null
  customer_master_key_spec           = "SYMMETRIC_DEFAULT"
  deletion_window_in_days            = null
  description                        = "tfstate encryption key for 415161a7 default environment"
  enable_key_rotation                = false
  is_enabled                         = true
  key_usage                          = "ENCRYPT_DECRYPT"
  multi_region                       = false
  policy = jsonencode({
    Id = "key-default-1"
    Statement = [{
      Action = "kms:*"
      Effect = "Allow"
      Principal = {
        AWS = "arn:aws:iam::141812438321:root"
      }
      Resource = "*"
      Sid      = "Enable IAM User Permissions"
    }]
    Version = "2012-10-17"
  })
  region                  = "us-east-1"
  rotation_period_in_days = 0
  tags = {
    Component    = "tfstate"
    Environment  = "default"
    ID           = "0"
    Name         = "415161a7-default-luther-tfstate-kms-0"
    Organization = "luther"
    Project      = "415161a7"
    Resource     = "kms"
  }
  tags_all = {
    Component    = "tfstate"
    Environment  = "default"
    ID           = "0"
    Name         = "415161a7-default-luther-tfstate-kms-0"
    Organization = "luther"
    Project      = "415161a7"
    Resource     = "kms"
  }
  xks_key_id = null
}

# __generated__ by Terraform
resource "aws_lb" "io_f_v6e_hzw_zt_alb" {
  client_keep_alive                           = 3600
  customer_owned_ipv4_pool                    = null
  desync_mitigation_mode                      = "defensive"
  dns_record_client_routing_policy            = null
  drop_invalid_header_fields                  = false
  enable_cross_zone_load_balancing            = true
  enable_deletion_protection                  = false
  enable_http2                                = true
  enable_tls_version_and_cipher_suite_headers = false
  enable_waf_fail_open                        = false
  enable_xff_client_port                      = false
  enable_zonal_shift                          = false
  idle_timeout                                = 60
  internal                                    = false
  ip_address_type                             = "ipv4"
  load_balancer_type                          = "application"
  name                                        = "io-f-v6e-hzw-zt-alb"
  preserve_host_header                        = false
  region                                      = "us-east-1"
  security_groups                             = ["sg-05b33367d0263c42d"]
  subnets                                     = ["subnet-08b4ceeaf7cbeccdb", "subnet-0a9e275a33c1d279f"]
  tags = {
    Component    = "insideout"
    Environment  = "prod"
    ID           = "alb0"
    Name         = "io-f-v6e-hzw-zt-prod-luthersystems-insideout-alb-alb0"
    Organization = "luthersystems"
    Project      = "io-f-v6e-hzw-zt"
    Resource     = "alb"
    Subcomponent = "alb"
  }
  tags_all = {
    Component    = "insideout"
    Environment  = "prod"
    ID           = "alb0"
    Name         = "io-f-v6e-hzw-zt-prod-luthersystems-insideout-alb-alb0"
    Organization = "luthersystems"
    Project      = "io-f-v6e-hzw-zt"
    Resource     = "alb"
    Subcomponent = "alb"
  }
  xff_header_processing_mode = "append"
  access_logs {
    bucket  = ""
    enabled = false
    prefix  = null
  }
  connection_logs {
    bucket  = ""
    enabled = false
    prefix  = null
  }
  health_check_logs {
    bucket  = ""
    enabled = false
    prefix  = null
  }
  subnet_mapping {
    allocation_id        = null
    ipv6_address         = null
    private_ipv4_address = null
    subnet_id            = "subnet-08b4ceeaf7cbeccdb"
  }
  subnet_mapping {
    allocation_id        = null
    ipv6_address         = null
    private_ipv4_address = null
    subnet_id            = "subnet-0a9e275a33c1d279f"
  }
}

# __generated__ by Terraform
resource "aws_sns_topic" "io_f_v6e_hzw_zt_prod_luthersystems_insideout_cwm_cwm0_alarms" {
  application_failure_feedback_role_arn    = null
  application_success_feedback_role_arn    = null
  application_success_feedback_sample_rate = 0
  archive_policy                           = null
  content_based_deduplication              = false
  delivery_policy                          = null
  display_name                             = null
  fifo_topic                               = false
  firehose_failure_feedback_role_arn       = null
  firehose_success_feedback_role_arn       = null
  firehose_success_feedback_sample_rate    = 0
  http_failure_feedback_role_arn           = null
  http_success_feedback_role_arn           = null
  http_success_feedback_sample_rate        = 0
  kms_master_key_id                        = null
  lambda_failure_feedback_role_arn         = null
  lambda_success_feedback_role_arn         = null
  lambda_success_feedback_sample_rate      = 0
  name                                     = "io-f-v6e-hzw-zt-prod-luthersystems-insideout-cwm-cwm0-alarms"
  policy = jsonencode({
    Id = "__default_policy_ID"
    Statement = [{
      Action = ["SNS:GetTopicAttributes", "SNS:SetTopicAttributes", "SNS:AddPermission", "SNS:RemovePermission", "SNS:DeleteTopic", "SNS:Subscribe", "SNS:ListSubscriptionsByTopic", "SNS:Publish"]
      Condition = {
        StringEquals = {
          "AWS:SourceOwner" = "141812438321"
        }
      }
      Effect = "Allow"
      Principal = {
        AWS = "*"
      }
      Resource = "arn:aws:sns:us-east-1:141812438321:io-f-v6e-hzw-zt-prod-luthersystems-insideout-cwm-cwm0-alarms"
      Sid      = "__default_statement_ID"
    }]
    Version = "2008-10-17"
  })
  region                           = "us-east-1"
  signature_version                = 0
  sqs_failure_feedback_role_arn    = null
  sqs_success_feedback_role_arn    = null
  sqs_success_feedback_sample_rate = 0
  tags = {
    Component    = "insideout"
    Environment  = "prod"
    ID           = "cwm0"
    Name         = "io-f-v6e-hzw-zt-prod-luthersystems-insideout-cwm-cwm0"
    Organization = "luthersystems"
    Project      = "io-f-v6e-hzw-zt"
    Resource     = "cwm"
    Subcomponent = "cwm"
  }
  tags_all = {
    Component    = "insideout"
    Environment  = "prod"
    ID           = "cwm0"
    Name         = "io-f-v6e-hzw-zt-prod-luthersystems-insideout-cwm-cwm0"
    Organization = "luthersystems"
    Project      = "io-f-v6e-hzw-zt"
    Resource     = "cwm"
    Subcomponent = "cwm"
  }
}

# __generated__ by Terraform from "/aws/containerinsights/io-hrbs5zprbk51-prod-lu-eks0/performance@us-east-1"
resource "aws_cloudwatch_log_group" "aws_containerinsights_io_hrbs5zprbk51_prod_lu_eks0_performance" {
  deletion_protection_enabled = false
  kms_key_id                  = null
  log_group_class             = "STANDARD"
  name                        = "/aws/containerinsights/io-hrbs5zprbk51-prod-lu-eks0/performance"
  region                      = "us-east-1"
  retention_in_days           = 0
  skip_destroy                = false
  tags                        = {}
  tags_all                    = {}
}

# __generated__ by Terraform from "/aws/rds/instance/io-hrbs5zprbk51-prod-luthersystems-insideout-rds-rds0-replica-1/postgresql@us-east-1"
resource "aws_cloudwatch_log_group" "aws_rds_instance_io_hrbs5zprbk51_prod_luthersystems_insideout_rds_rds0_replica_1_pos" {
  deletion_protection_enabled = false
  kms_key_id                  = null
  log_group_class             = "STANDARD"
  name                        = "/aws/rds/instance/io-hrbs5zprbk51-prod-luthersystems-insideout-rds-rds0-replica-1/postgresql"
  region                      = "us-east-1"
  retention_in_days           = 0
  skip_destroy                = false
  tags                        = {}
  tags_all                    = {}
}

# __generated__ by Terraform
resource "aws_route_table" "rtb_0c6948ecb0f73fadd" {
  propagating_vgws = []
  region           = "us-east-1"
  route = [{
    carrier_gateway_id         = ""
    cidr_block                 = "0.0.0.0/0"
    core_network_arn           = ""
    destination_prefix_list_id = ""
    egress_only_gateway_id     = ""
    gateway_id                 = "igw-0ac85034e2ee83ea0"
    ipv6_cidr_block            = ""
    local_gateway_id           = ""
    nat_gateway_id             = ""
    network_interface_id       = ""
    transit_gateway_id         = ""
    vpc_endpoint_id            = ""
    vpc_peering_connection_id  = ""
  }]
  tags     = {}
  tags_all = {}
  vpc_id   = "vpc-0648c27c4d5455bc7"
}

# __generated__ by Terraform from "io-f-v6e-hzw-zt-prod-luthersystems-insideout-cwm-cwm0@us-east-1"
resource "aws_cloudwatch_dashboard" "io_f_v6e_hzw_zt_prod_luthersystems_insideout_cwm_cwm0" {
  dashboard_body = jsonencode({
    widgets = [{
      h = 6
      properties = {
        metrics = [["AWS/ApplicationELB", "HTTPCode_Target_5XX_Count", {
          stat = "Sum"
          }], ["AWS/ApplicationELB", "HTTPCode_Target_5XX_Count", "LoadBalancer", "app/io-f-v6e-hzw-zt-alb/bcda3f52ff22fa50", {
          stat = "Sum"
          }], [".", "TargetResponseTime", {
          stat = "Average"
          }], ["AWS/ApplicationELB", "TargetResponseTime", "LoadBalancer", "app/io-f-v6e-hzw-zt-alb/bcda3f52ff22fa50", {
          stat = "Average"
        }]]
        period = 300
        region = "us-east-1"
        stat   = "Sum"
        title  = "ALB 5XX & Latency"
      }
      type = "metric"
      w    = 24
      x    = 0
      y    = 24
    }]
  })
  dashboard_name = "io-f-v6e-hzw-zt-prod-luthersystems-insideout-cwm-cwm0"
  region         = "us-east-1"
}

# __generated__ by Terraform from "sgr-0ed0af9ecdda77016@us-east-1"
resource "aws_vpc_security_group_egress_rule" "sgr_0ed0af9ecdda77016" {
  cidr_ipv4                    = "0.0.0.0/0"
  cidr_ipv6                    = null
  description                  = null
  from_port                    = null
  ip_protocol                  = "-1"
  prefix_list_id               = null
  referenced_security_group_id = null
  region                       = "us-east-1"
  security_group_id            = "sg-0003b998198cbbf54"
  tags                         = null
  to_port                      = null
}

# __generated__ by Terraform from "sgr-0aa94a92e442faa91@us-east-1"
resource "aws_vpc_security_group_ingress_rule" "sgr_0aa94a92e442faa91" {
  cidr_ipv4                    = "0.0.0.0/0"
  cidr_ipv6                    = null
  description                  = "Allow HTTPS"
  from_port                    = 443
  ip_protocol                  = "tcp"
  prefix_list_id               = null
  referenced_security_group_id = null
  region                       = "us-east-1"
  security_group_id            = "sg-05b33367d0263c42d"
  tags                         = null
  to_port                      = 443
}

# __generated__ by Terraform from "default.postgres15@us-east-1"
resource "aws_db_parameter_group" "default_postgres15" {
  description  = "Default parameter group for postgres15"
  family       = "postgres15"
  name         = "default.postgres15"
  region       = "us-east-1"
  skip_destroy = false
  tags         = {}
  tags_all     = {}
}

# __generated__ by Terraform
resource "aws_subnet" "subnet_0be95e0920e37ef2a" {
  assign_ipv6_address_on_creation                = false
  availability_zone                              = "us-east-1b"
  availability_zone_id                           = "use1-az2"
  cidr_block                                     = "10.1.16.0/20"
  customer_owned_ipv4_pool                       = null
  enable_dns64                                   = false
  enable_lni_at_device_index                     = 0
  enable_resource_name_dns_a_record_on_launch    = false
  enable_resource_name_dns_aaaa_record_on_launch = false
  ipv4_ipam_pool_id                              = null
  ipv4_netmask_length                            = null
  ipv6_ipam_pool_id                              = null
  ipv6_native                                    = false
  ipv6_netmask_length                            = null
  map_customer_owned_ip_on_launch                = false
  map_public_ip_on_launch                        = false
  outpost_arn                                    = null
  private_dns_hostname_type_on_launch            = "ip-name"
  region                                         = "us-east-1"
  tags = {
    Component                         = "insideout"
    Environment                       = "prod"
    ID                                = "vpc0"
    Name                              = "io-f0ttnkgjdvee-prod-luthersystems-insideout-vpc-vpc0"
    Organization                      = "luthersystems"
    Project                           = "io-f0ttnkgjdvee"
    Resource                          = "vpc"
    Subcomponent                      = "vpc"
    "kubernetes.io/role/internal-elb" = "1"
  }
  tags_all = {
    Component                         = "insideout"
    Environment                       = "prod"
    ID                                = "vpc0"
    Name                              = "io-f0ttnkgjdvee-prod-luthersystems-insideout-vpc-vpc0"
    Organization                      = "luthersystems"
    Project                           = "io-f0ttnkgjdvee"
    Resource                          = "vpc"
    Subcomponent                      = "vpc"
    "kubernetes.io/role/internal-elb" = "1"
  }
  vpc_id = "vpc-0b5b7a95d51782af5"
}

# __generated__ by Terraform from "igw-0dd9d251e758fc090@us-east-1"
resource "aws_internet_gateway" "igw_0dd9d251e758fc090" {
  region = "us-east-1"
  tags = {
    Component    = "insideout"
    Environment  = "prod"
    ID           = "vpc0"
    Name         = "io-f0ttnkgjdvee-prod-luthersystems-insideout-vpc-vpc0"
    Organization = "luthersystems"
    Project      = "io-f0ttnkgjdvee"
    Resource     = "vpc"
    Subcomponent = "vpc"
  }
  tags_all = {
    Component    = "insideout"
    Environment  = "prod"
    ID           = "vpc0"
    Name         = "io-f0ttnkgjdvee-prod-luthersystems-insideout-vpc-vpc0"
    Organization = "luthersystems"
    Project      = "io-f0ttnkgjdvee"
    Resource     = "vpc"
    Subcomponent = "vpc"
  }
  vpc_id = "vpc-0b5b7a95d51782af5"
}

# __generated__ by Terraform from "luther-55f50492-default-tfstate-s3-tbzt"
resource "aws_s3_bucket" "luther_55f50492_default_tfstate_s3_tbzt" {
  bucket              = "luther-55f50492-default-tfstate-s3-tbzt"
  bucket_namespace    = "global"
  force_destroy       = false
  object_lock_enabled = false
  region              = "us-east-1"
  tags = {
    Component   = "tfstate"
    Environment = "default"
    ID          = "tbzt"
    Name        = "luther-55f50492-default-tfstate-s3-tbzt"
    Project     = "55f50492"
    Resource    = "s3"
  }
  tags_all = {
    Component   = "tfstate"
    Environment = "default"
    ID          = "tbzt"
    Name        = "luther-55f50492-default-tfstate-s3-tbzt"
    Project     = "55f50492"
    Resource    = "s3"
  }
}

# __generated__ by Terraform
resource "aws_subnet" "subnet_08b4ceeaf7cbeccdb" {
  assign_ipv6_address_on_creation                = false
  availability_zone                              = "us-east-1a"
  availability_zone_id                           = "use1-az1"
  cidr_block                                     = "10.1.128.0/20"
  customer_owned_ipv4_pool                       = null
  enable_dns64                                   = false
  enable_lni_at_device_index                     = 0
  enable_resource_name_dns_a_record_on_launch    = false
  enable_resource_name_dns_aaaa_record_on_launch = false
  ipv4_ipam_pool_id                              = null
  ipv4_netmask_length                            = null
  ipv6_ipam_pool_id                              = null
  ipv6_native                                    = false
  ipv6_netmask_length                            = null
  map_customer_owned_ip_on_launch                = false
  map_public_ip_on_launch                        = true
  outpost_arn                                    = null
  private_dns_hostname_type_on_launch            = "ip-name"
  region                                         = "us-east-1"
  tags = {
    Component                = "insideout"
    Environment              = "prod"
    ID                       = "vpc0"
    Name                     = "io-f-v6e-hzw-zt-prod-luthersystems-insideout-vpc-vpc0"
    Organization             = "luthersystems"
    Project                  = "io-f-v6e-hzw-zt"
    Resource                 = "vpc"
    Subcomponent             = "vpc"
    "kubernetes.io/role/elb" = "1"
  }
  tags_all = {
    Component                = "insideout"
    Environment              = "prod"
    ID                       = "vpc0"
    Name                     = "io-f-v6e-hzw-zt-prod-luthersystems-insideout-vpc-vpc0"
    Organization             = "luthersystems"
    Project                  = "io-f-v6e-hzw-zt"
    Resource                 = "vpc"
    Subcomponent             = "vpc"
    "kubernetes.io/role/elb" = "1"
  }
  vpc_id = "vpc-0328efde06fc443f8"
}

# __generated__ by Terraform
resource "aws_ebs_volume" "vol_0307d249a67afc994" {
  availability_zone          = "us-east-1a"
  encrypted                  = true
  final_snapshot             = null
  iops                       = 3000
  kms_key_id                 = "arn:aws:kms:us-east-1:141812438321:key/cb27d9ef-4b39-4604-978c-5160e4a0427b"
  multi_attach_enabled       = false
  outpost_arn                = null
  region                     = "us-east-1"
  size                       = 20
  snapshot_id                = "snap-0659b41d0af1d883a"
  tags                       = {}
  tags_all                   = {}
  throughput                 = 125
  type                       = "gp3"
  volume_initialization_rate = 0
}

# __generated__ by Terraform from "sg-07ab0fd93782eb359@us-east-1"
resource "aws_security_group" "io_f0ttnkgjdvee_prod_luthersystems_insideout_ec2_ec20_sg" {
  description = "Security group for io-f0ttnkgjdvee EC2 instance"
  egress = [{
    cidr_blocks      = ["0.0.0.0/0"]
    description      = ""
    from_port        = 0
    ipv6_cidr_blocks = []
    prefix_list_ids  = []
    protocol         = "-1"
    security_groups  = []
    self             = false
    to_port          = 0
  }]
  ingress = [{
    cidr_blocks      = ["18.206.107.24/29"]
    description      = "SSH from EC2 Instance Connect service IPs"
    from_port        = 22
    ipv6_cidr_blocks = []
    prefix_list_ids  = []
    protocol         = "tcp"
    security_groups  = []
    self             = false
    to_port          = 22
  }]
  name                   = "io-f0ttnkgjdvee-prod-luthersystems-insideout-ec2-ec20-sg"
  region                 = "us-east-1"
  revoke_rules_on_delete = null
  tags = {
    Component    = "insideout"
    Environment  = "prod"
    ID           = "ec20"
    Name         = "io-f0ttnkgjdvee-prod-luthersystems-insideout-ec2-sg"
    Organization = "luthersystems"
    Project      = "io-f0ttnkgjdvee"
    Resource     = "ec2"
    Subcomponent = "ec2"
  }
  tags_all = {
    Component    = "insideout"
    Environment  = "prod"
    ID           = "ec20"
    Name         = "io-f0ttnkgjdvee-prod-luthersystems-insideout-ec2-sg"
    Organization = "luthersystems"
    Project      = "io-f0ttnkgjdvee"
    Resource     = "ec2"
    Subcomponent = "ec2"
  }
  vpc_id = "vpc-0b5b7a95d51782af5"
}

# __generated__ by Terraform from "https://sqs.us-east-1.amazonaws.com/141812438321/io-f-v6e-hzw-zt-queue-dlq@us-east-1"
resource "aws_sqs_queue" "io_f_v6e_hzw_zt_queue_dlq" {
  content_based_deduplication       = false
  delay_seconds                     = 0
  fifo_queue                        = false
  kms_data_key_reuse_period_seconds = 300
  kms_master_key_id                 = null
  max_message_size                  = 262144
  message_retention_seconds         = 1209600
  name                              = "io-f-v6e-hzw-zt-queue-dlq"
  receive_wait_time_seconds         = 0
  region                            = "us-east-1"
  sqs_managed_sse_enabled           = true
  tags = {
    Component    = "insideout"
    Environment  = "prod"
    ID           = "sqs0"
    Name         = "io-f-v6e-hzw-zt-prod-luthersystems-insideout-sqs-sqs0"
    Organization = "luthersystems"
    Project      = "io-f-v6e-hzw-zt"
    Resource     = "sqs"
    Subcomponent = "sqs"
  }
  tags_all = {
    Component    = "insideout"
    Environment  = "prod"
    ID           = "sqs0"
    Name         = "io-f-v6e-hzw-zt-prod-luthersystems-insideout-sqs-sqs0"
    Organization = "luthersystems"
    Project      = "io-f-v6e-hzw-zt"
    Resource     = "sqs"
    Subcomponent = "sqs"
  }
  visibility_timeout_seconds = 30
}

# __generated__ by Terraform from "/io-f-v6e-hzw-zt-prod-luthersystems-insideout-cwl-cwl95b9/app@us-east-1"
resource "aws_cloudwatch_log_group" "io_f_v6e_hzw_zt_prod_luthersystems_insideout_cwl_cwl95b9_app" {
  deletion_protection_enabled = false
  kms_key_id                  = null
  log_group_class             = "STANDARD"
  name                        = "/io-f-v6e-hzw-zt-prod-luthersystems-insideout-cwl-cwl95b9/app"
  region                      = "us-east-1"
  retention_in_days           = 30
  skip_destroy                = false
  tags = {
    Component    = "insideout"
    Environment  = "prod"
    ID           = "cwl95b9"
    Name         = "io-f-v6e-hzw-zt-prod-luthersystems-insideout-cwl-cwl95b9"
    Organization = "luthersystems"
    Project      = "io-f-v6e-hzw-zt"
    Resource     = "cwl"
    Subcomponent = "cwl"
  }
  tags_all = {
    Component    = "insideout"
    Environment  = "prod"
    ID           = "cwl95b9"
    Name         = "io-f-v6e-hzw-zt-prod-luthersystems-insideout-cwl-cwl95b9"
    Organization = "luthersystems"
    Project      = "io-f-v6e-hzw-zt"
    Resource     = "cwl"
    Subcomponent = "cwl"
  }
}

# __generated__ by Terraform from "io-j-auxxec7qa--ec2-profile"
resource "aws_iam_instance_profile" "io_j_auxxec7qa_ec2_profile" {
  name     = "io-j-auxxec7qa--ec2-profile"
  path     = "/"
  role     = "io-j-auxxec7qa--ec2-role"
  tags     = {}
  tags_all = {}
}

# __generated__ by Terraform from "rtb-075f7e9835359fccf@us-east-1"
resource "aws_route_table" "rtb_075f7e9835359fccf" {
  propagating_vgws = []
  region           = "us-east-1"
  route            = []
  tags = {
    Component    = "insideout"
    Environment  = "prod"
    ID           = "vpc0"
    Name         = "io-f0ttnkgjdvee-prod-luthersystems-insideout-vpc-vpc0"
    Organization = "luthersystems"
    Project      = "io-f0ttnkgjdvee"
    Resource     = "vpc"
    Subcomponent = "vpc"
  }
  tags_all = {
    Component    = "insideout"
    Environment  = "prod"
    ID           = "vpc0"
    Name         = "io-f0ttnkgjdvee-prod-luthersystems-insideout-vpc-vpc0"
    Organization = "luthersystems"
    Project      = "io-f0ttnkgjdvee"
    Resource     = "vpc"
    Subcomponent = "vpc"
  }
  vpc_id = "vpc-0b5b7a95d51782af5"
}

# __generated__ by Terraform
resource "aws_kms_alias" "alias_aws_dynamodb" {
  name          = "alias/aws/dynamodb"
  region        = "us-east-1"
  target_key_id = "4cbb8c7d-bf43-4d62-aea0-91e336b364ce"
}

# __generated__ by Terraform from "/aws/containerinsights/io-hrbs5zprbk51-prod-lu-eks0/application@us-east-1"
resource "aws_cloudwatch_log_group" "aws_containerinsights_io_hrbs5zprbk51_prod_lu_eks0_application" {
  deletion_protection_enabled = false
  kms_key_id                  = null
  log_group_class             = "STANDARD"
  name                        = "/aws/containerinsights/io-hrbs5zprbk51-prod-lu-eks0/application"
  region                      = "us-east-1"
  retention_in_days           = 0
  skip_destroy                = false
  tags                        = {}
  tags_all                    = {}
}

# __generated__ by Terraform from "luther-55f50492-default-tfstate-s3-tbzt@us-east-1"
resource "aws_s3_bucket_server_side_encryption_configuration" "luther_55f50492_default_tfstate_s3_tbzt_sse" {
  bucket = "luther-55f50492-default-tfstate-s3-tbzt"
  region = "us-east-1"
  rule {
    blocked_encryption_types = ["SSE-C"]
    bucket_key_enabled       = false
    apply_server_side_encryption_by_default {
      kms_master_key_id = "arn:aws:kms:us-east-1:141812438321:key/9b00e98c-0de7-480e-bd81-ed2e84a5f1b9"
      sse_algorithm     = "aws:kms"
    }
  }
}

# __generated__ by Terraform from "rtb-0df25055f73b98433@us-east-1"
resource "aws_route_table" "rtb_0df25055f73b98433" {
  propagating_vgws = []
  region           = "us-east-1"
  route            = []
  tags = {
    Component    = "insideout"
    Environment  = "prod"
    ID           = "vpc0"
    Name         = "io-f-v6e-hzw-zt-prod-luthersystems-insideout-vpc-vpc0"
    Organization = "luthersystems"
    Project      = "io-f-v6e-hzw-zt"
    Resource     = "vpc"
    Subcomponent = "vpc"
  }
  tags_all = {
    Component    = "insideout"
    Environment  = "prod"
    ID           = "vpc0"
    Name         = "io-f-v6e-hzw-zt-prod-luthersystems-insideout-vpc-vpc0"
    Organization = "luthersystems"
    Project      = "io-f-v6e-hzw-zt"
    Resource     = "vpc"
    Subcomponent = "vpc"
  }
  vpc_id = "vpc-0328efde06fc443f8"
}

# __generated__ by Terraform
resource "aws_vpc" "vpc_0328efde06fc443f8" {
  assign_generated_ipv6_cidr_block     = false
  cidr_block                           = "10.1.0.0/16"
  enable_dns_hostnames                 = true
  enable_dns_support                   = true
  enable_network_address_usage_metrics = false
  instance_tenancy                     = "default"
  ipv4_ipam_pool_id                    = null
  ipv4_netmask_length                  = null
  ipv6_ipam_pool_id                    = null
  ipv6_netmask_length                  = 0
  region                               = "us-east-1"
  tags = {
    Component    = "insideout"
    Environment  = "prod"
    ID           = "vpc0"
    Name         = "io-f-v6e-hzw-zt-prod-luthersystems-insideout-vpc-vpc0"
    Organization = "luthersystems"
    Project      = "io-f-v6e-hzw-zt"
    Resource     = "vpc"
    Subcomponent = "vpc"
  }
  tags_all = {
    Component    = "insideout"
    Environment  = "prod"
    ID           = "vpc0"
    Name         = "io-f-v6e-hzw-zt-prod-luthersystems-insideout-vpc-vpc0"
    Organization = "luthersystems"
    Project      = "io-f-v6e-hzw-zt"
    Resource     = "vpc"
    Subcomponent = "vpc"
  }
}

# __generated__ by Terraform from "io-f-v6e-hzw-zt-app@us-east-1"
resource "aws_dynamodb_table" "io_f_v6e_hzw_zt_app" {
  billing_mode                = "PAY_PER_REQUEST"
  deletion_protection_enabled = false
  hash_key                    = "pk"
  name                        = "io-f-v6e-hzw-zt-app"
  range_key                   = null
  read_capacity               = 0
  region                      = "us-east-1"
  restore_backup_arn          = null
  restore_date_time           = null
  restore_source_name         = null
  restore_source_table_arn    = null
  restore_to_latest_time      = null
  stream_enabled              = false
  table_class                 = "STANDARD"
  tags = {
    Component    = "insideout"
    Environment  = "prod"
    ID           = "dynamodb0"
    Name         = "io-f-v6e-hzw-zt-prod-luthersystems-insideout-dynamodb-dynamodb0"
    Organization = "luthersystems"
    Project      = "io-f-v6e-hzw-zt"
    Resource     = "dynamodb"
    Subcomponent = "dynamodb"
  }
  tags_all = {
    Component    = "insideout"
    Environment  = "prod"
    ID           = "dynamodb0"
    Name         = "io-f-v6e-hzw-zt-prod-luthersystems-insideout-dynamodb-dynamodb0"
    Organization = "luthersystems"
    Project      = "io-f-v6e-hzw-zt"
    Resource     = "dynamodb"
    Subcomponent = "dynamodb"
  }
  write_capacity = 0
  attribute {
    name = "pk"
    type = "S"
  }
  point_in_time_recovery {
    enabled                 = true
    recovery_period_in_days = 35
  }
  server_side_encryption {
    enabled = true
  }
  ttl {
    attribute_name = null
    enabled        = false
  }
}

# __generated__ by Terraform from "arn:aws:secretsmanager:us-east-1:141812438321:secret:io-f0ttnkgjdvee-prod-luthersystems-insideout-sm-sm0a38-YrSC3C@us-east-1"
resource "aws_secretsmanager_secret" "io_f0ttnkgjdvee_prod_luthersystems_insideout_sm_sm0a38" {
  description                    = null
  force_overwrite_replica_secret = null
  kms_key_id                     = null
  name                           = "io-f0ttnkgjdvee-prod-luthersystems-insideout-sm-sm0a38"
  recovery_window_in_days        = null
  region                         = "us-east-1"
  tags = {
    Component    = "insideout"
    Environment  = "prod"
    ID           = "sm0a38"
    Name         = "io-f0ttnkgjdvee-prod-luthersystems-insideout-sm-0"
    Organization = "luthersystems"
    Project      = "io-f0ttnkgjdvee"
    Resource     = "sm"
    Subcomponent = "sm"
  }
  tags_all = {
    Component    = "insideout"
    Environment  = "prod"
    ID           = "sm0a38"
    Name         = "io-f0ttnkgjdvee-prod-luthersystems-insideout-sm-0"
    Organization = "luthersystems"
    Project      = "io-f0ttnkgjdvee"
    Resource     = "sm"
    Subcomponent = "sm"
  }
}

# __generated__ by Terraform from "arn:aws:secretsmanager:us-east-1:141812438321:secret:io-f-v6e-hzw-zt-prod-luthersystems-insideout-sm-sm0132-qKfJh4@us-east-1"
resource "aws_secretsmanager_secret" "io_f_v6e_hzw_zt_prod_luthersystems_insideout_sm_sm0132" {
  description                    = null
  force_overwrite_replica_secret = null
  kms_key_id                     = null
  name                           = "io-f-v6e-hzw-zt-prod-luthersystems-insideout-sm-sm0132"
  recovery_window_in_days        = null
  region                         = "us-east-1"
  tags = {
    Component    = "insideout"
    Environment  = "prod"
    ID           = "sm0132"
    Name         = "io-f-v6e-hzw-zt-prod-luthersystems-insideout-sm-0"
    Organization = "luthersystems"
    Project      = "io-f-v6e-hzw-zt"
    Resource     = "sm"
    Subcomponent = "sm"
  }
  tags_all = {
    Component    = "insideout"
    Environment  = "prod"
    ID           = "sm0132"
    Name         = "io-f-v6e-hzw-zt-prod-luthersystems-insideout-sm-0"
    Organization = "luthersystems"
    Project      = "io-f-v6e-hzw-zt"
    Resource     = "sm"
    Subcomponent = "sm"
  }
}

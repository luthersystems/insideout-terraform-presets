# AWS App Runner service preset (#598 Row 2).
#
# Mirrors the role gcp/cloud_run plays for GCP stacks: a single-call entry
# point for a managed-container service on AWS. The preset provisions:
#
#   - An App Runner service (`aws_apprunner_service`) named
#     `${var.project}-${var.service_name}` so the InsideOut inspector can
#     attribute it to the stack via name-prefix scoping (CLAUDE.md
#     "name-prefix scoping" rule).
#   - A versioned autoscaling configuration
#     (`aws_apprunner_auto_scaling_configuration_version`) bound to the
#     service. The configuration is a separate top-level resource whose
#     name changes on any field mutation; `create_before_destroy` plus
#     name suffixing keep replace-cycles clean instead of orphaning
#     versions that block the service delete.
#   - An ECR access IAM role (`aws_iam_role.access`) — only created when
#     `image_repository_type = "ECR"`. App Runner's build plane assumes it
#     to pull from private ECR. Carries the `aws:SourceAccount`
#     confused-deputy guard (matches aws/sagemaker + aws/bedrock).
#   - An instance IAM role (`aws_iam_role.instance`) the running tasks
#     assume. Default trust policy only; callers attach app-specific
#     policies out-of-band via the role name/ARN outputs.
#   - Optional VPC connector + matching security group for private egress
#     (`aws_apprunner_vpc_connector` + `aws_security_group.vpc_connector`)
#     when `enable_vpc_connector = true`.
#   - Optional custom domain association
#     (`aws_apprunner_custom_domain_association`) when
#     `custom_domain_name != null`. AWS validates the cert asynchronously
#     post-apply — the preset returns the DNS-01 validation records as an
#     output so callers can complete the manual step in their DNS provider.
#
# Scope ceiling: matches gcp/cloud_run simplicity. Source-code based
# services (vs. image), tracing/observability bindings, and KMS-encrypted
# secrets are exposed only as the AWS provider defaults — advanced
# consumers wrap or fork.
#
# Discovery inspector: deferred per #598 (issue marks discovery inspector
# as optional follow-up for every parity row). See
# `pkg/observability/extractors/extractors_drift_test.go` allowlist.

terraform {
  required_version = ">= 1.5"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 6.0"
    }
    random = {
      source  = "hashicorp/random"
      version = ">= 3.5"
    }
  }
}

# Standard luthername-driven naming + tag set. Every AWS preset in the
# repo wires through this module so the InsideOut inspector's exact
# `Project = <project>` filter (CLAUDE.md issue #81) catches every
# resource.
module "name" {
  source         = "github.com/luthersystems/tf-modules.git//luthername?ref=v55.15.0"
  luther_project = var.project
  aws_region     = var.region
  luther_env     = var.environment
  org_name       = "luthersystems"
  component      = "insideout"
  subcomponent   = "ar"
  resource       = "ar"
}

# Caller identity drives the aws:SourceAccount confused-deputy guard on
# the App Runner service-role trust policies (matches aws/sagemaker +
# aws/bedrock).
data "aws_caller_identity" "current" {}

locals {
  # Project-tag merge from the luthername module so every taggable
  # resource carries the full standard tag set.
  tags = merge(module.name.tags, var.tags)

  service_name = "${var.project}-${var.service_name}"

  needs_access_role   = var.image_repository_type == "ECR"
  needs_vpc_connector = var.enable_vpc_connector

  # Autoscaling-config name suffix per-create so a CPU/memory mutation
  # rolls the version forward via create_before_destroy without
  # name-collision on the new version.
  autoscaling_name = "${var.project}-${var.service_name}-${random_id.autoscaling_suffix.hex}"
}

resource "random_id" "autoscaling_suffix" {
  byte_length = 3
}

# -----------------------------------------------------------------------------
# IAM access role (ECR pull) — only when pulling from private ECR.
# -----------------------------------------------------------------------------

resource "aws_iam_role" "access" {
  count = local.needs_access_role ? 1 : 0

  name = "${var.project}-apprunner-access"
  path = "/service-role/"

  # build.apprunner.amazonaws.com is the App Runner build plane
  # service principal — distinct from the tasks.apprunner.amazonaws.com
  # used on the instance role below. Scoped to the deploying account
  # via aws:SourceAccount so a hostile cross-account caller can't
  # trick App Runner into using this role on their behalf.
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Principal = {
        Service = "build.apprunner.amazonaws.com"
      }
      Action = "sts:AssumeRole"
      Condition = {
        StringEquals = {
          "aws:SourceAccount" = data.aws_caller_identity.current.account_id
        }
      }
    }]
  })

  tags = local.tags

  lifecycle {
    ignore_changes = [managed_policy_arns]
  }
}

resource "aws_iam_role_policy_attachment" "access_ecr" {
  count = local.needs_access_role ? 1 : 0

  role       = aws_iam_role.access[0].name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AWSAppRunnerServicePolicyForECRAccess"
}

# -----------------------------------------------------------------------------
# IAM instance role — assumed by running tasks. Callers attach app-specific
# permissions out-of-band via the role name/ARN outputs.
# -----------------------------------------------------------------------------

resource "aws_iam_role" "instance" {
  name = "${var.project}-apprunner-instance"
  path = "/service-role/"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Principal = {
        Service = "tasks.apprunner.amazonaws.com"
      }
      Action = "sts:AssumeRole"
      Condition = {
        StringEquals = {
          "aws:SourceAccount" = data.aws_caller_identity.current.account_id
        }
      }
    }]
  })

  tags = local.tags

  lifecycle {
    ignore_changes = [managed_policy_arns]
  }
}

# -----------------------------------------------------------------------------
# Optional VPC connector for private egress.
#
# The matching security group has no ingress rules (App Runner doesn't
# accept inbound from the customer VPC — only the service URL takes
# traffic) and a single egress-all rule so the running tasks can reach
# whatever private resource the caller is wiring (RDS, ElastiCache,
# OpenSearch, internal ALB, etc.).
# -----------------------------------------------------------------------------

resource "aws_security_group" "vpc_connector" {
  count = local.needs_vpc_connector ? 1 : 0

  name        = "${var.project}-apprunner-vpcconnector"
  description = "App Runner VPC connector egress for ${local.service_name}"
  vpc_id      = var.vpc_id

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
    description = "Allow all egress from the App Runner VPC connector"
  }

  tags = local.tags
}

resource "aws_apprunner_vpc_connector" "main" {
  count = local.needs_vpc_connector ? 1 : 0

  vpc_connector_name = "${var.project}-apprunner-vpc"
  subnets            = var.subnet_ids
  security_groups    = [aws_security_group.vpc_connector[0].id]

  tags = local.tags
}

# -----------------------------------------------------------------------------
# Autoscaling configuration.
#
# App Runner versions every autoscaling config — any change spawns a new
# version. We suffix the configuration_name with a random_id so the
# create_before_destroy lifecycle can replace versions cleanly when a
# caller tunes min/max/concurrency. Without the suffix, the new version
# would collide on the existing name and the apply would fail.
# -----------------------------------------------------------------------------

resource "aws_apprunner_auto_scaling_configuration_version" "main" {
  auto_scaling_configuration_name = local.autoscaling_name

  max_concurrency = var.max_concurrency
  max_size        = var.max_size
  min_size        = var.min_size

  tags = local.tags

  lifecycle {
    create_before_destroy = true
  }
}

# -----------------------------------------------------------------------------
# App Runner service.
# -----------------------------------------------------------------------------

resource "aws_apprunner_service" "main" {
  service_name = local.service_name

  auto_scaling_configuration_arn = aws_apprunner_auto_scaling_configuration_version.main.arn

  source_configuration {
    auto_deployments_enabled = var.auto_deployments_enabled

    dynamic "authentication_configuration" {
      for_each = local.needs_access_role ? [1] : []
      content {
        access_role_arn = aws_iam_role.access[0].arn
      }
    }

    image_repository {
      image_identifier      = var.image_repository_url
      image_repository_type = var.image_repository_type

      image_configuration {
        port = tostring(var.port)

        runtime_environment_variables = var.env_vars
      }
    }
  }

  instance_configuration {
    cpu               = var.cpu
    memory            = var.memory
    instance_role_arn = aws_iam_role.instance.arn
  }

  health_check_configuration {
    protocol = var.health_check_protocol
    # The HTTP path field is rejected by the API when protocol = TCP. The
    # dynamic block below would be cleaner but App Runner's health_check
    # block requires `path` to be present even for TCP — the value is
    # just ignored. Provider validation accepts a non-empty default.
    path     = var.health_check_path
    interval = var.health_check_interval_seconds
  }

  network_configuration {
    ingress_configuration {
      is_publicly_accessible = var.is_publicly_accessible
    }

    dynamic "egress_configuration" {
      for_each = local.needs_vpc_connector ? [1] : []
      content {
        egress_type       = "VPC"
        vpc_connector_arn = aws_apprunner_vpc_connector.main[0].arn
      }
    }
  }

  tags = local.tags
}

# -----------------------------------------------------------------------------
# Optional custom domain association.
#
# AWS validates the cert asynchronously after the apply returns —
# `certificate_validation_records` is populated in the apply output so
# callers can add the DNS-01 records in their DNS provider. The
# association stays in `pending_certificate_dns_validation` status until
# DNS resolves; the service URL keeps working in the meantime.
# -----------------------------------------------------------------------------

resource "aws_apprunner_custom_domain_association" "main" {
  count = var.custom_domain_name == null ? 0 : 1

  domain_name          = var.custom_domain_name
  service_arn          = aws_apprunner_service.main.arn
  enable_www_subdomain = var.enable_www_subdomain
}

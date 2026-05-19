variable "project" {
  description = "Naming / Project-tag prefix for stack resources. The InsideOut inspector filters AWS resources by exact `Project = <project>` match — this value also seeds the App Runner service name (`<project>-<service_name>`) and IAM role names. Capped at 40 chars so the longest derived name (`<project>-apprunner-vpcconnector`, 25 fixed chars + 6-char random suffix on the autoscaling config) stays inside AWS's 64-char identifier limits."
  type        = string
  default     = "demo"

  validation {
    condition     = length(trimspace(var.project)) > 0
    error_message = "project must be a non-empty string."
  }

  validation {
    condition     = length(var.project) <= 40
    error_message = "project must be 40 chars or fewer so derived IAM role / VPC connector / autoscaling-config names fit inside AWS's 64-char identifier limits."
  }
}

variable "region" {
  description = "AWS region. Passed into the luthername module so the standard tag set carries the region. The AWS provider itself picks the region up from provider config."
  type        = string
  default     = "us-east-1"
}

variable "environment" {
  description = "Deployment environment (e.g. production, staging, sandbox). Feeds the luthername module's standard tag set; not used elsewhere in the preset."
  type        = string
  default     = "sandbox"

  validation {
    condition     = length(trimspace(var.environment)) > 0
    error_message = "environment must be a non-empty string."
  }
}

variable "tags" {
  description = "Extra tags merged onto every taggable resource. The preset always sets the standard luthername tag set (including `Project = var.project`); entries here override or extend that base set."
  type        = map(string)
  default     = {}
}

# -----------------------------------------------------------------------------
# Service shape
# -----------------------------------------------------------------------------

variable "service_name" {
  description = "App Runner service name component. The composed service name is `<project>-<service_name>` (matches the gcp/cloud_run pattern of project-prefixed naming for inspector attribution)."
  type        = string
  default     = "app"

  validation {
    condition     = can(regex("^[A-Za-z][A-Za-z0-9_-]{1,38}[A-Za-z0-9]$", var.service_name))
    error_message = "service_name must be 3-40 chars, start with a letter, end alphanumeric, contain only letters/digits/underscores/hyphens (App Runner naming rule)."
  }
}

variable "image_repository_url" {
  description = "Container image identifier. For ECR private (image_repository_type = ECR), use the full repository URI with a tag (e.g. `123456789012.dkr.ecr.us-east-1.amazonaws.com/myapp:latest`). For ECR Public, use `public.ecr.aws/<alias>/<image>:<tag>`. Defaults to AWS's hello-app on ECR Public so a fresh deploy comes up green."
  type        = string
  default     = "public.ecr.aws/aws-containers/hello-app-runner:latest"
}

variable "image_repository_type" {
  description = "Source type for image_repository_url. `ECR` (private) pulls via an access IAM role the preset creates. `ECR_PUBLIC` pulls anonymously and the access role is NOT created."
  type        = string
  default     = "ECR_PUBLIC"

  validation {
    condition     = contains(["ECR", "ECR_PUBLIC"], var.image_repository_type)
    error_message = "image_repository_type must be one of: ECR, ECR_PUBLIC."
  }
}

variable "port" {
  description = "TCP port the container listens on. App Runner ingress forwards HTTPS traffic to this port. Default 8080 matches the hello-app and most managed-runtime defaults."
  type        = number
  default     = 8080

  validation {
    condition     = var.port >= 1 && var.port <= 65535
    error_message = "port must be in the range 1-65535."
  }
}

variable "env_vars" {
  description = "Runtime environment variables injected into the container. Reserved App Runner prefixes (AWSAPPRUNNER, AWS_APPRUNNER) are rejected by the API at apply time."
  type        = map(string)
  default     = {}
}

# -----------------------------------------------------------------------------
# Compute sizing.
#
# App Runner only accepts a fixed set of CPU/memory pairs. We validate each
# axis independently here; the AWS API will reject invalid pairs at apply
# time with a clear error message.
# https://docs.aws.amazon.com/apprunner/latest/dg/architecture.html#architecture.instances
# -----------------------------------------------------------------------------

variable "cpu" {
  description = "vCPU allocation. App Runner accepts one of: 0.25 vCPU, 0.5 vCPU, 1 vCPU, 2 vCPU, 4 vCPU. The numeric-string form (`256`, `512`, `1024`, `2048`, `4096`) is also accepted by the provider."
  type        = string
  default     = "1 vCPU"

  validation {
    condition     = contains(["0.25 vCPU", "0.5 vCPU", "1 vCPU", "2 vCPU", "4 vCPU", "256", "512", "1024", "2048", "4096"], var.cpu)
    error_message = "cpu must be one of: 0.25 vCPU, 0.5 vCPU, 1 vCPU, 2 vCPU, 4 vCPU (or numeric equivalents 256, 512, 1024, 2048, 4096)."
  }
}

variable "memory" {
  description = "Memory allocation. App Runner accepts one of: 0.5 GB, 1 GB, 2 GB, 3 GB, 4 GB, 6 GB, 8 GB, 10 GB, 12 GB. The numeric-string form in MB is also accepted by the provider."
  type        = string
  default     = "2 GB"

  validation {
    condition     = contains(["0.5 GB", "1 GB", "2 GB", "3 GB", "4 GB", "6 GB", "8 GB", "10 GB", "12 GB", "512", "1024", "2048", "3072", "4096", "6144", "8192", "10240", "12288"], var.memory)
    error_message = "memory must be one of: 0.5 GB, 1 GB, 2 GB, 3 GB, 4 GB, 6 GB, 8 GB, 10 GB, 12 GB (or numeric MB equivalents). Note: not every cpu/memory pair is valid — App Runner will reject invalid pairs at apply time."
  }
}

# -----------------------------------------------------------------------------
# Autoscaling
# -----------------------------------------------------------------------------

variable "min_size" {
  description = "Minimum number of provisioned instances. App Runner keeps at least this many instances warm; 1 is the lowest the API accepts (App Runner does not support scale-to-zero today)."
  type        = number
  default     = 1

  validation {
    condition     = var.min_size >= 1 && var.min_size <= 25
    error_message = "min_size must be in the range 1-25 (App Runner scaling limits)."
  }
}

variable "max_size" {
  description = "Maximum number of instances App Runner will scale out to. Hard ceiling is 25 per service today."
  type        = number
  default     = 10

  validation {
    condition     = var.max_size >= 1 && var.max_size <= 25
    error_message = "max_size must be in the range 1-25 (App Runner scaling limits)."
  }
}

variable "max_concurrency" {
  description = "Maximum concurrent requests per instance before App Runner scales out. App Runner accepts 1-200; default 100 matches App Runner's own default."
  type        = number
  default     = 100

  validation {
    condition     = var.max_concurrency >= 1 && var.max_concurrency <= 200
    error_message = "max_concurrency must be in the range 1-200."
  }
}

# -----------------------------------------------------------------------------
# Ingress / public accessibility
# -----------------------------------------------------------------------------

variable "is_publicly_accessible" {
  description = "Whether the service URL is reachable from the public internet. Default true matches gcp/cloud_run's `allow_unauthenticated = true` default — flip to false for a VPC-internal service."
  type        = bool
  default     = true
}

variable "auto_deployments_enabled" {
  description = "Whether App Runner auto-deploys on new ECR image pushes. Default false to keep deploys explicit; flip to true for CI-driven continuous deployment from an ECR tag."
  type        = bool
  default     = false
}

# -----------------------------------------------------------------------------
# Health check
# -----------------------------------------------------------------------------

variable "health_check_protocol" {
  description = "Health check protocol. `HTTP` requires `health_check_path`; `TCP` only checks port reachability."
  type        = string
  default     = "TCP"

  validation {
    condition     = contains(["HTTP", "TCP"], var.health_check_protocol)
    error_message = "health_check_protocol must be one of: HTTP, TCP."
  }
}

variable "health_check_path" {
  description = "HTTP health check path. Only consulted when health_check_protocol = HTTP, but the App Runner API requires the field to be non-empty in all cases (it's ignored for TCP)."
  type        = string
  default     = "/"
}

variable "health_check_interval_seconds" {
  description = "Health check interval in seconds. App Runner accepts 1-20."
  type        = number
  default     = 10

  validation {
    condition     = var.health_check_interval_seconds >= 1 && var.health_check_interval_seconds <= 20
    error_message = "health_check_interval_seconds must be in the range 1-20."
  }
}

# -----------------------------------------------------------------------------
# Optional VPC connector for private egress.
#
# vpc_id + subnet_ids are normally wired by the composer's DefaultWiring
# from module.aws_vpc when KeyAWSVPC is selected. They're required by AWS
# provider only when enable_vpc_connector = true; the preset gates the
# resource creation on the flag, so leaving them empty (the composer's
# preview-safe stubs) on a public-only service is fine.
# -----------------------------------------------------------------------------

variable "enable_vpc_connector" {
  description = "Whether to provision an App Runner VPC connector for private egress (RDS, ElastiCache, internal ALB, etc.). When true, vpc_id + subnet_ids must point at the customer VPC; the preset creates the connector + a matching security group (egress-all, no ingress) and wires the service's egress_configuration to it."
  type        = bool
  default     = false
}

variable "vpc_id" {
  description = "VPC ID for the App Runner VPC connector. Only consulted when enable_vpc_connector = true. Composer wires this from `module.aws_vpc.vpc_id` automatically when KeyAWSVPC is selected (which is the ImplicitDependencies default for KeyAWSAppRunner)."
  type        = string
  default     = ""
}

variable "subnet_ids" {
  description = "Subnet IDs the VPC connector attaches ENIs into. Only consulted when enable_vpc_connector = true. Use private subnets for actual private egress; public subnets give you no isolation benefit. Composer wires this from `module.aws_vpc.private_subnet_ids` automatically when KeyAWSVPC is selected."
  type        = list(string)
  default     = []
}

# -----------------------------------------------------------------------------
# Optional custom domain
# -----------------------------------------------------------------------------

variable "custom_domain_name" {
  description = "Custom domain to associate with the service (e.g. `app.example.com`). Null disables the association. AWS validates the cert asynchronously post-apply — the DNS validation records land on the `custom_domain_validation_records` output; the caller is responsible for adding them in their DNS provider for the cert to issue."
  type        = string
  default     = null

  validation {
    condition     = var.custom_domain_name == null ? true : can(regex("^([a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\\.)+[a-zA-Z]{2,}$", var.custom_domain_name))
    error_message = "custom_domain_name must be a valid FQDN (e.g. app.example.com) when set."
  }
}

variable "enable_www_subdomain" {
  description = "Whether to also associate the `www.<custom_domain_name>` subdomain with the service. Only consulted when custom_domain_name != null."
  type        = bool
  default     = false
}

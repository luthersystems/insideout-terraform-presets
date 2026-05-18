variable "project" {
  description = "Naming / Project-tag prefix for stack resources. The InsideOut inspector filters AWS resources by exact `Project = <project>` match — this value also seeds the SageMaker Studio domain name (`<project>-studio`) so label-less attribution works."
  type        = string
  default     = "demo"

  validation {
    condition     = length(trimspace(var.project)) > 0
    error_message = "project must be a non-empty string."
  }
}

variable "region" {
  description = "AWS region. Declared so the composer's namespaced wiring (`sagemaker_region`) sees a target — the AWS provider itself picks region up from provider config, so this value is informational at the module level."
  type        = string
  default     = "us-east-1"
}

variable "tags" {
  description = "Extra tags merged onto every taggable resource. The preset always sets `Project = var.project`; entries here override or extend that base set."
  type        = map(string)
  default     = {}
}

# -----------------------------------------------------------------------------
# Networking (required — AWS provider 6.x demands vpc_id + subnet_ids on
# every SageMaker domain regardless of access mode)
# -----------------------------------------------------------------------------

variable "vpc_id" {
  description = "VPC ID the SageMaker domain attaches to. Required by AWS provider 6.x for every domain (even in PublicInternetOnly mode the Studio app ENIs land in your VPC). Composer wires this from `module.aws_vpc.vpc_id` automatically when KeyAWSVPC is selected."
  type        = string

  validation {
    condition     = length(trimspace(var.vpc_id)) > 0
    error_message = "vpc_id must be a non-empty string."
  }
}

variable "subnet_ids" {
  description = "Subnet IDs the Studio domain attaches to. Required (non-empty). For VpcOnly mode use private subnets; for PublicInternetOnly mode either tier works (the ENIs need outbound only). Composer wires this from `module.aws_vpc.private_subnet_ids` automatically when KeyAWSVPC is selected."
  type        = list(string)

  validation {
    condition     = length(var.subnet_ids) > 0
    error_message = "subnet_ids must contain at least one subnet ID."
  }
}

variable "network_mode" {
  description = "SageMaker Studio app network access type. `PublicInternetOnly` (default) keeps egress through AWS-managed networking; `VpcOnly` forces all egress through the customer VPC (requires NAT or VPC endpoints for the SageMaker control plane)."
  type        = string
  default     = "PublicInternetOnly"

  validation {
    condition     = contains(["PublicInternetOnly", "VpcOnly"], var.network_mode)
    error_message = "network_mode must be one of: PublicInternetOnly, VpcOnly."
  }
}

# -----------------------------------------------------------------------------
# Workspace S3 bucket
# -----------------------------------------------------------------------------

variable "workspace_bucket" {
  description = "Caller-supplied S3 bucket name to use as the Studio workspace. Null lets the preset create one named `<project>-sagemaker-workspace` (versioning + AES256 + public-access-block, all on)."
  type        = string
  default     = null

  validation {
    condition     = var.workspace_bucket == null ? true : can(regex("^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$", var.workspace_bucket))
    error_message = "workspace_bucket must be a valid S3 bucket name (3-63 chars, lowercase alphanumeric / hyphens / dots, start+end alphanumeric)."
  }
}

variable "workspace_bucket_force_destroy" {
  description = "Whether `force_destroy` is set on the preset-managed workspace bucket. Only meaningful when `workspace_bucket == null`. Default false matches AWS console behavior — `terraform destroy` fails on a non-empty bucket."
  type        = bool
  default     = false
}

# -----------------------------------------------------------------------------
# Studio user profiles
# -----------------------------------------------------------------------------

variable "studio_users" {
  description = "List of Studio user-profile names to provision under the domain. Empty list = domain-only (admins create users out-of-band)."
  type        = list(string)
  default     = []

  validation {
    condition = alltrue([
      for u in var.studio_users :
      can(regex("^[a-zA-Z0-9](-*[a-zA-Z0-9])*$", u)) && length(u) <= 63
    ])
    error_message = "Every studio_users entry must be 1-63 chars, alphanumeric or hyphen, not starting/ending with a hyphen (SageMaker user-profile naming rule)."
  }
}

# -----------------------------------------------------------------------------
# IAM policy override
# -----------------------------------------------------------------------------

variable "sagemaker_managed_policy_arn" {
  description = "AWS-managed policy ARN attached to the Studio execution role. Defaults to `AmazonSageMakerFullAccess` (broad — required for general Studio usage). Override with a scoped-down policy ARN in locked-down environments."
  type        = string
  default     = "arn:aws:iam::aws:policy/AmazonSageMakerFullAccess"

  validation {
    condition     = can(regex("^arn:aws[a-zA-Z-]*:iam::(aws|[0-9]{12}):policy/", var.sagemaker_managed_policy_arn))
    error_message = "sagemaker_managed_policy_arn must be a valid IAM policy ARN (arn:aws:iam::aws:policy/... or arn:aws:iam::<account>:policy/...)."
  }
}

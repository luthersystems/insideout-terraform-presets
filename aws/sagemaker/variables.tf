variable "project" {
  description = "Naming / Project-tag prefix for stack resources. The InsideOut inspector filters AWS resources by exact `Project = <project>` match — this value also seeds the SageMaker Studio domain name (`<project>-studio`) so label-less attribution works. Capped at 35 chars so the preset-created workspace bucket name (`<project>-sagemaker-workspace-<6hex>`, 28 fixed chars) stays inside S3's 63-char hard limit."
  type        = string
  default     = "demo"

  validation {
    condition     = length(trimspace(var.project)) > 0
    error_message = "project must be a non-empty string."
  }

  validation {
    condition     = length(var.project) <= 35
    error_message = "project must be 35 chars or fewer so the preset-created S3 workspace bucket name fits inside the 63-char AWS limit (project + `-sagemaker-workspace-<6hex>` = 28 fixed chars)."
  }
}

variable "region" {
  description = "AWS region. Passed into the luthername module so the standard tag set carries the region, and threaded through to S3 ARN partition resolution. The AWS provider itself picks region up from provider config."
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

variable "home_efs_retention" {
  description = "What to do with the Studio domain's auto-provisioned home EFS filesystem when the domain is destroyed. `Delete` (default) removes the EFS so its mount-target ENIs release and the enclosing VPC can be torn down cleanly — without this, AWS's default `Retain` orphans the EFS and its ENIs, which wedge VPC subnet deletion for ~20min and then fail. Set `Retain` to keep Studio home directories across a domain replace (you must then clean the EFS out-of-band)."
  type        = string
  default     = "Delete"

  validation {
    condition     = contains(["Delete", "Retain"], var.home_efs_retention)
    error_message = "home_efs_retention must be one of: Delete, Retain."
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
# Real-time inference endpoint (#761) — gated on enable_inference. When off,
# the preset stays Studio-only (the #615 behavior); when on, it adds a
# model + endpoint-configuration + endpoint trio hosting a servable container.
# -----------------------------------------------------------------------------

variable "enable_inference" {
  description = "When true, provision a real-time inference slice (aws_sagemaker_model + aws_sagemaker_endpoint_configuration + aws_sagemaker_endpoint) hosting model_image. When false (default) the preset stays Studio-only and none of the inference resources are created. The Studio domain + execution role are always provisioned regardless."
  type        = bool
  default     = false
}

variable "model_image" {
  description = "ECR / SageMaker container image URI for the model's primary container (e.g. a Deep Learning Container or an LLM serving image). Required (non-empty) when enable_inference is true — SageMaker cannot host a model without a servable container. Ignored when enable_inference is false."
  type        = string
  default     = ""
}

variable "model_data_url" {
  description = "Optional S3 URI to the model artifact tarball (model.tar.gz) loaded into the container at startup. Leave empty for images that bundle their own weights (many LLM serving images pull from the hub at runtime). Only consumed when enable_inference is true."
  type        = string
  default     = ""

  validation {
    # Require a bucket AND a key, not a bare `s3://` (which would derive an
    # empty bucket ARN and 403 the model-data read at apply). Bucket segment:
    # 3-63 chars, lowercase alphanumeric / hyphen / dot, start+end
    # alphanumeric (S3 naming). Key segment: at least one non-slash char so
    # `s3://bucket/` (bucket but no object) is also rejected.
    condition     = var.model_data_url == "" ? true : can(regex("^s3://[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]/.+", var.model_data_url))
    error_message = "model_data_url must be a full s3://<bucket>/<key> URI pointing at the model artifact (or empty to let the container supply its own weights). A bare s3:// or bucket-only s3://<bucket>/ is rejected."
  }
}

variable "model_environment" {
  description = "Environment variables injected into the model's primary container (a map of name → value). Threads straight through to the SageMaker `primary_container.environment`. Use it to configure the serving image — e.g. an AWS HuggingFace DLC needs HF_MODEL_ID + HF_TASK to know which hub model to pull and how to serve it; an LMI/TGI image takes its own model/engine vars here. Empty (default) leaves the container's own defaults in place. Only consumed when enable_inference is true. Not marked sensitive — these are serving knobs, not secrets; pass real secrets via a secrets manager, not container env."
  type        = map(string)
  default     = {}
  sensitive   = false
}

variable "inference_iam_propagation_delay" {
  description = "How long to wait after writing the endpoint's execution-role grants before creating the endpoint, to let IAM propagate to SageMaker's control plane. Without this delay CreateEndpoint fires before the role is validated, and the provider's create-retry then collides on `Cannot create already existing endpoint` (observed on a real-account run). A Go duration string (e.g. `30s`, `1m`). Only consumed when enable_inference is true."
  type        = string
  default     = "30s"

  validation {
    condition     = can(regex("^[0-9]+(s|m|h)$", var.inference_iam_propagation_delay))
    error_message = "inference_iam_propagation_delay must be a Go duration like 30s, 1m, or 1h."
  }
}

variable "endpoint_instance_type" {
  description = "Instance type for the real-time endpoint's production variant (e.g. ml.m5.xlarge, ml.g5.xlarge for GPU LLM serving). Must be an ml.* family — SageMaker hosting only accepts ml-prefixed instance types. Only consumed when enable_inference is true."
  type        = string
  default     = "ml.m5.xlarge"

  validation {
    # SageMaker hosting instance types are always ml.<family>.<size>. A bare
    # EC2 type (m5.xlarge) or a typo is rejected at plan so callers don't
    # discover it only when the endpoint create fails at apply.
    condition     = can(regex("^ml\\.[a-z0-9]+\\.[a-z0-9]+$", var.endpoint_instance_type))
    error_message = "endpoint_instance_type must be a SageMaker ml.* hosting instance type (e.g. ml.m5.xlarge, ml.g5.xlarge)."
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

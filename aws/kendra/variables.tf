variable "project" {
  description = "Project name"
  type        = string

  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{1,61}[a-z0-9]$", var.project))
    error_message = "Project must be lowercase alphanumeric with hyphens, 3-63 characters."
  }
}

variable "region" {
  description = "AWS region"
  type        = string
}

variable "environment" {
  description = "Deployment environment (e.g. dev, staging, prod). Surfaces in the luthername module's tags."
  type        = string
  default     = "dev"
}

variable "tags" {
  description = "Additional resource tags merged over the module's standard Project tags."
  type        = map(string)
  default     = {}
}

# --- Index --------------------------------------------------------------------

variable "index_name" {
  description = "Name of the Kendra index. Defaults to \"{project}-index\" when null."
  type        = string
  default     = null

  validation {
    # Kendra index names allow [a-zA-Z0-9_-], 1-1000 chars; constrain to a
    # reasonable length. Enforce when set so an invalid name fails at plan time.
    condition     = var.index_name == null ? true : can(regex("^[a-zA-Z0-9_-]{1,100}$", var.index_name))
    error_message = "index_name must be 1-100 characters of letters, digits, hyphens, or underscores."
  }
}

variable "edition" {
  description = "Kendra index edition. DEVELOPER_EDITION (cost-friendly, single-node, ~$1.125/hr) or ENTERPRISE_EDITION (HA, production). IMMUTABLE — changing it replaces the index."
  type        = string
  default     = "DEVELOPER_EDITION"

  validation {
    # Pin to the two editions this preset supports. GEN_AI_ENTERPRISE_EDITION
    # exists in the provider but is out of scope for this component.
    condition     = contains(["DEVELOPER_EDITION", "ENTERPRISE_EDITION"], var.edition)
    error_message = "edition must be \"DEVELOPER_EDITION\" or \"ENTERPRISE_EDITION\"."
  }
}

variable "user_context_policy" {
  description = "How Kendra enforces document-level access control at query time. ATTRIBUTE_FILTER (default) filters by user/group attributes supplied per query; USER_TOKEN uses tokens from the index's user-token configuration."
  type        = string
  default     = "ATTRIBUTE_FILTER"

  validation {
    condition     = contains(["ATTRIBUTE_FILTER", "USER_TOKEN"], var.user_context_policy)
    error_message = "user_context_policy must be \"ATTRIBUTE_FILTER\" or \"USER_TOKEN\"."
  }
}

variable "kms_key_id" {
  description = "Optional customer-managed KMS key id/ARN for index encryption at rest. When null, Kendra encrypts with an AWS-owned key."
  type        = string
  default     = null
}

variable "iam_propagation_delay" {
  description = "How long to wait for the index/data-source IAM role + policy to propagate before Kendra validates them on create. Tunable for slow-propagation accounts."
  type        = string
  default     = "20s"
}

# --- S3 data source -----------------------------------------------------------

variable "s3_bucket_name" {
  description = "Name of the S3 bucket Kendra crawls as a document source. When null no S3 data source, its access role, or its policy are created — a bare index with documents ingested out-of-band. In a composed stack DefaultWiring supplies this from module.aws_s3.bucket_name when aws_s3 is also selected."
  type        = string
  default     = null

  validation {
    condition     = var.s3_bucket_name == null ? true : can(regex("^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$", var.s3_bucket_name))
    error_message = "s3_bucket_name must be a valid S3 bucket name (3-63 lowercase chars) or null."
  }
}

variable "s3_bucket_arn" {
  description = "Optional ARN of the S3 bucket named by s3_bucket_name, used to scope the data-source access policy least-privilege. When null it is derived from s3_bucket_name. In a composed stack DefaultWiring supplies this from module.aws_s3.bucket_arn."
  type        = string
  default     = null

  validation {
    condition     = var.s3_bucket_arn == null ? true : can(regex("^arn:aws[a-zA-Z-]*:s3:::", var.s3_bucket_arn))
    error_message = "s3_bucket_arn must be an S3 bucket ARN (arn:aws:s3:::...) or null."
  }
}

variable "s3_crawl_schedule" {
  description = "Optional cron schedule (e.g. \"cron(0 6 * * ? *)\") for the S3 connector's periodic re-crawl. When null the connector only syncs on-demand."
  type        = string
  default     = null
}

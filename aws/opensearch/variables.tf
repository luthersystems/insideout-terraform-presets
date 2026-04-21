variable "project" {
  type        = string
  description = "Project name for resource naming. Used as a prefix for the OpenSearch domain name (managed) or AOSS collection name (serverless). OpenSearch Service caps domain names at 28 chars; AOSS caps collection/policy names at 32. The module appends '-search' (7 chars), so the tighter managed-mode constraint of 21 applies."
  validation {
    condition     = length(trimspace(var.project)) > 0 && length(var.project) <= 21
    error_message = "project must be a non-empty string ≤21 characters. OpenSearch Service domain names cap at 28 chars; this module appends '-search' (7), so project must be ≤21 to satisfy both managed and serverless modes."
  }
  # Project is interpolated into the aws_cloudwatch_log_resource_policy ARN
  # wildcard below; restrict to characters safe in both IAM policy ARNs and
  # CloudWatch log-group names so a value like "*" or "foo/bar" cannot widen
  # the policy scope. Aligns with OpenSearch domain name rules (a-z 0-9 -).
  validation {
    condition     = can(regex("^[a-z0-9][a-z0-9-]*$", var.project))
    error_message = "project must match ^[a-z0-9][a-z0-9-]*$ — lowercase alphanumerics and hyphens, starting with alphanumeric. Matches OpenSearch domain name rules and prevents IAM-policy-ARN widening via the log resource policy."
  }
}

variable "environment" {
  description = "Deployment environment (e.g. production, staging, sandbox)"
  type        = string
  validation {
    condition     = length(trimspace(var.environment)) > 0
    error_message = "environment must be a non-empty string."
  }
}

variable "region" {
  type        = string
  description = "AWS region"
}

variable "vpc_id" {
  type        = string
  description = "VPC ID for the managed-mode OpenSearch domain's VPC interface. Required in managed mode; ignored in serverless mode."
  default     = null
}

variable "subnet_ids" {
  type        = list(string)
  description = "Subnet IDs for the managed-mode OpenSearch domain. Required in managed mode; ignored in serverless mode."
  default     = []
}

variable "deployment_type" {
  type        = string
  description = "Deployment type. \"managed\" provisions an OpenSearch Service domain in the VPC; \"serverless\" provisions an OpenSearch Serverless collection. Must be lowercase."
  default     = "managed"
  validation {
    condition     = contains(["managed", "serverless"], var.deployment_type)
    error_message = "deployment_type must be either \"managed\" or \"serverless\" (lowercase)."
  }
}

variable "instance_type" {
  type        = string
  description = "OpenSearch instance type"
  default     = "t3.medium.search"
}

variable "storage_size" {
  type        = string
  description = "Storage size in GB"
  default     = "10GB"
}

variable "multi_az" {
  type        = bool
  description = "Whether to enable Multi-AZ deployment"
  default     = false
}

variable "tags" {
  description = "Additional AWS tags applied to all resources"
  type        = map(string)
  default     = {}
}

variable "kms_key_arn" {
  type        = string
  description = "Optional KMS key ARN for the AOSS encryption security policy. If null (default), the AWS-owned AOSS key is used. Serverless mode only."
  default     = null

  # Empty string would take the customer-KMS arm of the encryption policy
  # and emit `"KmsARN":""`, which AOSS rejects. Force null for the default.
  # Ternary (not `||`) because Terraform validation does not short-circuit —
  # `trimspace(null)` would blow up before the null check fires.
  validation {
    condition     = var.kms_key_arn == null ? true : length(trimspace(var.kms_key_arn)) > 0
    error_message = "kms_key_arn must be null or a non-empty ARN string."
  }
}

variable "allow_public_access" {
  type        = bool
  description = "AOSS network security policy: when true (default), the collection and dashboards are reachable from the public internet. Set false only if the stack provisions an aws_opensearchserverless_vpc_endpoint (not included in this module). Serverless mode only."
  default     = true
}

variable "log_retention_days" {
  description = "Retention (days) for the CloudWatch log groups holding managed-mode OpenSearch index/search slow logs and application logs. Must be one of CloudWatch's supported retention values. Managed mode only."
  type        = number
  default     = 30
  validation {
    condition     = contains([1, 3, 5, 7, 14, 30, 60, 90, 120, 150, 180, 365, 400, 545, 731, 1096, 1827, 2192, 2557, 2922, 3288, 3653], var.log_retention_days)
    error_message = "log_retention_days must be one of CloudWatch Logs' supported retention values: 1, 3, 5, 7, 14, 30, 60, 90, 120, 150, 180, 365, 400, 545, 731, 1096, 1827, 2192, 2557, 2922, 3288, 3653."
  }
}

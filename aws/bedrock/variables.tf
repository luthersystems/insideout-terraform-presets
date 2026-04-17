variable "project" {
  type        = string
  description = "Project name for resource naming"
}

variable "region" {
  type        = string
  description = "AWS region"
}

variable "environment" {
  description = "Deployment environment (e.g. production, staging, sandbox)"
  type        = string
  validation {
    condition     = length(trimspace(var.environment)) > 0
    error_message = "environment must be a non-empty string."
  }
}

variable "model_id" {
  type        = string
  description = "Bedrock foundation model ID the role may invoke (chat/completions). Granted via the IAM policy."
  default     = "anthropic.claude-3-sonnet-20240229-v1:0"
}

variable "embedding_model_id" {
  type        = string
  description = "Bedrock embedding model ID the role may invoke. Granted via the IAM policy so the application can ingest into a Knowledge Base backed by this role."
  default     = "amazon.titan-embed-text-v1"
}

variable "s3_bucket_arn" {
  type        = string
  description = "ARN of the S3 bucket the role is granted s3:ListBucket and s3:GetObject on. Required — the Bedrock KB role has no meaningful purpose without an S3 data source."
}

variable "opensearch_collection_arn" {
  type        = string
  description = "ARN of the OpenSearch Serverless (AOSS) collection that backs the Bedrock Knowledge Base vector store. Managed-domain ARNs are not supported by Bedrock. Required — this role exists specifically to grant aoss:APIAccessAll on this collection."
  validation {
    condition     = can(regex("^arn:aws[a-z-]*:aoss:[a-z0-9-]+:[0-9]{12}:collection/[a-z0-9]+$", var.opensearch_collection_arn))
    error_message = "opensearch_collection_arn must be an AOSS collection ARN matching arn:aws:aoss:<region>:<account>:collection/<id>."
  }
}

variable "tags" {
  description = "Additional AWS tags applied to all resources"
  type        = map(string)
  default     = {}
}

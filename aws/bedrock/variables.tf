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

variable "knowledge_base_name" {
  type        = string
  description = "Name of the Bedrock Knowledge Base"
  default     = "default-kb"
}

variable "model_id" {
  type        = string
  description = "Bedrock Model ID for the Knowledge Base"
  default     = "anthropic.claude-3-sonnet-20240229-v1:0"
}

variable "embedding_model_id" {
  type        = string
  description = "Bedrock Embedding Model ID"
  default     = "amazon.titan-embed-text-v1"
}

variable "s3_bucket_arn" {
  type        = string
  description = "ARN of the S3 bucket for the data source"
  default     = null
}

variable "opensearch_collection_arn" {
  type        = string
  description = "ARN of the OpenSearch Serverless (AOSS) collection that backs the Bedrock Knowledge Base vector store. Managed-domain ARNs are not supported by Bedrock."
  default     = null
  validation {
    condition     = var.opensearch_collection_arn == null ? true : can(regex("^arn:aws[a-z-]*:aoss:[a-z0-9-]+:[0-9]{12}:collection/[a-z0-9]+$", var.opensearch_collection_arn))
    error_message = "opensearch_collection_arn must be an AOSS collection ARN matching arn:aws:aoss:<region>:<account>:collection/<id>."
  }
}

variable "tags" {
  description = "Additional AWS tags applied to all resources"
  type        = map(string)
  default     = {}
}

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

variable "opensearch_arn" {
  type        = string
  description = "ARN of the OpenSearch domain/collection for the vector store"
  default     = null
}

variable "tags" {
  description = "Additional AWS tags applied to all resources"
  type        = map(string)
  default     = {}
}

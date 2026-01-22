variable "project" {
  type        = string
  description = "Project name for resource naming"
}

variable "region" {
  type        = string
  description = "AWS region"
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

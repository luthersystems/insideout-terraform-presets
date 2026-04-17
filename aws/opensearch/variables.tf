variable "project" {
  type        = string
  description = "Project name for resource naming"
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
  description = "VPC ID for OpenSearch domain"
}

variable "subnet_ids" {
  type        = list(string)
  description = "List of subnet IDs for OpenSearch domain"
}

variable "deployment_type" {
  type        = string
  description = "Deployment type (Managed or Serverless)"
  default     = "managed"
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

variable "data_access_principal_arns" {
  type        = list(string)
  description = "List of IAM role/user ARNs granted aoss:* on the collection and its indexes via a data-access policy. Serverless mode only. When non-empty, the Terraform caller's underlying role ARN is also added so it can create the vector index."
  default     = []
}

variable "create_bedrock_vector_index" {
  type        = bool
  description = "When true (and deployment_type is serverless), create the bedrock-knowledge-base-default-index vector index with the k-NN field mapping required by aws_bedrockagent_knowledge_base. Requires data_access_principal_arns to include the Bedrock KB role."
  default     = false
}

variable "kms_key_arn" {
  type        = string
  description = "Optional KMS key ARN for the AOSS encryption security policy. If null (default), the AWS-owned AOSS key is used. Serverless mode only."
  default     = null
}

variable "allow_public_access" {
  type        = bool
  description = "AOSS network security policy: when true (default), the collection and dashboards are reachable from the public internet. Set false only if the stack provisions an aws_opensearchserverless_vpc_endpoint (not included in this module). Serverless mode only."
  default     = true
}

variable "vector_embedding_dimension" {
  type        = number
  description = "Dimension of the k-NN vector field on the Bedrock default index. Default 1024 matches amazon.titan-embed-text-v1. Change if you switch embedding_model_id in the Bedrock module."
  default     = 1024
}


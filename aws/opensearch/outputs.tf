output "opensearch_arn" {
  value       = var.deployment_type == "managed" ? aws_opensearch_domain.managed[0].arn : aws_opensearchserverless_collection.serverless[0].arn
  description = "The ARN of the OpenSearch domain or collection"
}

output "opensearch_endpoint" {
  value       = var.deployment_type == "managed" ? aws_opensearch_domain.managed[0].endpoint : aws_opensearchserverless_collection.serverless[0].collection_endpoint
  description = "The endpoint of the OpenSearch domain or collection"
}


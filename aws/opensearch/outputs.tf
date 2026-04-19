output "opensearch_arn" {
  value       = var.deployment_type == "managed" ? aws_opensearch_domain.managed[0].arn : aws_opensearchserverless_collection.serverless[0].arn
  description = "ARN of the OpenSearch domain (managed) or collection (serverless)."
}

output "opensearch_endpoint" {
  value       = var.deployment_type == "managed" ? aws_opensearch_domain.managed[0].endpoint : aws_opensearchserverless_collection.serverless[0].collection_endpoint
  description = "Endpoint of the OpenSearch domain or AOSS collection."
}

output "collection_arn" {
  value       = var.deployment_type == "serverless" ? aws_opensearchserverless_collection.serverless[0].arn : null
  description = "ARN of the AOSS collection. null when deployment_type is managed. Wire this (not opensearch_arn) into aws/bedrock.opensearch_collection_arn. The application layer is responsible for creating the vector index against this collection."
}

output "collection_name" {
  value       = var.deployment_type == "serverless" ? aws_opensearchserverless_collection.serverless[0].name : null
  description = "Name of the AOSS collection (not the ID embedded in the ARN). null when deployment_type is managed. Wire into aws/bedrock.opensearch_collection_name so bedrock can author the AOSS data-access policy granting its role data-plane access — AOSS access policies match collections by name, not ARN."
}

output "opensearch_arn" {
  value       = var.deployment_type == "managed" ? aws_opensearch_domain.managed[0].arn : aws_opensearchserverless_collection.serverless[0].arn
  description = "The ARN of the OpenSearch domain or collection"
}

output "opensearch_endpoint" {
  value       = var.deployment_type == "managed" ? aws_opensearch_domain.managed[0].endpoint : aws_opensearchserverless_collection.serverless[0].collection_endpoint
  description = "The endpoint of the OpenSearch domain or collection"
}

output "collection_arn" {
  value       = var.deployment_type == "serverless" ? aws_opensearchserverless_collection.serverless[0].arn : null
  description = "ARN of the AOSS collection. null when deployment_type is managed. Wire this (not opensearch_arn) into aws/bedrock.opensearch_collection_arn. Consuming this output implicitly waits for the data-access policy and (if enabled) the Bedrock vector index to be created, so downstream Bedrock KB creation does not need its own depends_on."
  depends_on = [
    aws_opensearchserverless_access_policy.data,
    opensearch_index.bedrock_default,
  ]
}

output "vector_index_ready" {
  value       = var.create_bedrock_vector_index && var.deployment_type == "serverless" ? opensearch_index.bedrock_default[0].id : null
  description = "ID of the Bedrock default vector index when it has been created; null otherwise. Non-null value indicates the collection is fully ready for aws_bedrockagent_knowledge_base creation."
}

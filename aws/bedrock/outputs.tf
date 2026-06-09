output "role_arn" {
  value       = aws_iam_role.bedrock_kb.arn
  description = "ARN of the IAM role the application assumes when creating a Bedrock Knowledge Base against the configured AOSS collection and S3 bucket."
}

output "role_name" {
  value       = aws_iam_role.bedrock_kb.name
  description = "Name of the Bedrock IAM role. Useful when the application needs to attach additional policies at runtime."
}

output "aoss_data_access_policy_name" {
  value       = var.opensearch_collection_name == null ? null : aws_opensearchserverless_access_policy.bedrock[0].name
  description = "Name of the AOSS data-access policy granting the bedrock role + any additional principals data-plane access on the collection. null when opensearch_collection_name was not wired in."
}

output "invocation_log_group_name" {
  value       = var.enable_invocation_logging ? aws_cloudwatch_log_group.invocations[0].name : null
  description = "CloudWatch log group receiving Bedrock InvokeModel logs. null when enable_invocation_logging is false."
}

output "invocation_log_group_arn" {
  value       = var.enable_invocation_logging ? aws_cloudwatch_log_group.invocations[0].arn : null
  description = "ARN of the invocation log group. null when disabled. Wire into observability dashboards/alarms."
}

output "invocation_logging_role_arn" {
  value       = var.enable_invocation_logging ? aws_iam_role.invocation_logging[0].arn : null
  description = "ARN of the IAM role Bedrock assumes to write invocation logs. null when disabled."
}

output "knowledge_base_id" {
  value       = var.enable_knowledge_base ? aws_bedrockagent_knowledge_base.this[0].id : null
  description = "ID of the Bedrock Knowledge Base. null when enable_knowledge_base is false. Pass to aws_bedrockagent_agent_knowledge_base_association or RetrieveAndGenerate at runtime."
}

output "knowledge_base_arn" {
  value       = var.enable_knowledge_base ? aws_bedrockagent_knowledge_base.this[0].arn : null
  description = "ARN of the Bedrock Knowledge Base. null when disabled."
}

output "data_source_id" {
  value       = var.enable_knowledge_base ? aws_bedrockagent_data_source.s3_docs[0].data_source_id : null
  description = "ID of the S3 data source attached to the Knowledge Base. null when disabled. Pass to StartIngestionJob to (re)ingest the S3 docs bucket."
}

output "s3_vectors_bucket_arn" {
  value       = local.use_s3vectors ? aws_s3vectors_vector_bucket.kb[0].vector_bucket_arn : null
  description = "ARN of the in-module S3 Vectors bucket backing the Knowledge Base. null unless vector_store=s3vectors and the KB is enabled."
}

output "s3_vectors_index_arn" {
  value       = local.use_s3vectors ? aws_s3vectors_index.kb[0].index_arn : null
  description = "ARN of the in-module S3 Vectors index. null unless vector_store=s3vectors and the KB is enabled."
}

output "guardrail_id" {
  value       = var.enable_guardrail ? aws_bedrock_guardrail.this[0].guardrail_id : null
  description = "ID of the Bedrock guardrail. null when disabled. Pass to InvokeModel/Converse along with guardrail_version to apply this policy at runtime."
}

output "guardrail_arn" {
  value       = var.enable_guardrail ? aws_bedrock_guardrail.this[0].guardrail_arn : null
  description = "ARN of the Bedrock guardrail. null when disabled."
}

output "guardrail_version" {
  value       = var.enable_guardrail ? aws_bedrock_guardrail.this[0].version : null
  description = "Version string of the guardrail (DRAFT initially). Publish a numbered version with a separate aws_bedrock_guardrail_version resource if you need versioned releases."
}

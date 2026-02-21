output "knowledge_base_id" {
  value       = aws_bedrockagent_knowledge_base.this.id
  description = "The ID of the Bedrock Knowledge Base"
}

output "knowledge_base_arn" {
  value       = aws_bedrockagent_knowledge_base.this.arn
  description = "The ARN of the Bedrock Knowledge Base"
}

output "data_source_id" {
  value       = aws_bedrockagent_data_source.this.id
  description = "The ID of the Bedrock data source"
}

output "role_arn" {
  value       = aws_iam_role.bedrock_kb.arn
  description = "The ARN of the Bedrock Knowledge Base IAM role"
}

output "knowledge_base_id" {
  value       = aws_bedrockagent_knowledge_base.this.id
  description = "The ID of the Bedrock Knowledge Base"
}

output "knowledge_base_arn" {
  value       = aws_bedrockagent_knowledge_base.this.arn
  description = "The ARN of the Bedrock Knowledge Base"
}

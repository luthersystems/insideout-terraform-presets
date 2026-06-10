output "agent_id" {
  value       = aws_bedrockagent_agent.this.agent_id
  description = "ID of the Bedrock Agent. Pass to InvokeAgent (with agent_alias_id) at runtime."
}

output "agent_arn" {
  value       = aws_bedrockagent_agent.this.agent_arn
  description = "ARN of the Bedrock Agent."
}

output "agent_alias_id" {
  value       = aws_bedrockagent_agent_alias.this.agent_alias_id
  description = "ID of the agent's live alias. The stable handle to pass to InvokeAgent — always bound to the latest PREPARED version."
}

output "agent_alias_arn" {
  value       = aws_bedrockagent_agent_alias.this.agent_alias_arn
  description = "ARN of the agent's live alias."
}

output "agent_version" {
  value       = aws_bedrockagent_agent.this.agent_version
  description = "Current version of the agent (DRAFT until a numbered version is published)."
}

output "agent_resource_role_arn" {
  value       = aws_iam_role.agent.arn
  description = "ARN of the IAM role Bedrock assumes to run the agent (InvokeModel, + Retrieve when a Knowledge Base is associated)."
}

output "action_group_id" {
  value       = local.has_action_group ? aws_bedrockagent_agent_action_group.this[0].action_group_id : null
  description = "ID of the Lambda-backed action group. null when no action_group_lambda_arn was wired in (chat-only agent)."
}

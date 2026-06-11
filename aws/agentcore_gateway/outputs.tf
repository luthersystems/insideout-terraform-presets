output "gateway_id" {
  value       = aws_bedrockagentcore_gateway.this.gateway_id
  description = "ID of the AgentCore gateway."
}

output "gateway_arn" {
  value       = aws_bedrockagentcore_gateway.this.gateway_arn
  description = "ARN of the AgentCore gateway."
}

output "gateway_url" {
  value       = aws_bedrockagentcore_gateway.this.gateway_url
  description = "MCP endpoint URL agents/MCP clients connect to. Callers present a JWT from the configured issuer (jwt_discovery_url) to authenticate."
}

output "gateway_target_id" {
  value       = local.has_lambda_target ? aws_bedrockagentcore_gateway_target.lambda[0].target_id : null
  description = "ID of the Lambda MCP-tool target. null when no target_lambda_arn was wired in (gateway with externally-supplied targets only)."
}

output "gateway_role_arn" {
  value       = aws_iam_role.gateway.arn
  description = "ARN of the IAM role the gateway assumes to invoke its targets (carries lambda:InvokeFunction on the wired Lambda when a target is present)."
}

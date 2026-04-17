output "role_arn" {
  value       = aws_iam_role.bedrock_kb.arn
  description = "ARN of the IAM role the application assumes when creating a Bedrock Knowledge Base against the configured AOSS collection and S3 bucket."
}

output "role_name" {
  value       = aws_iam_role.bedrock_kb.name
  description = "Name of the Bedrock IAM role. Useful when the application needs to attach additional policies at runtime."
}

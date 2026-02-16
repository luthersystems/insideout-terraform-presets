output "function_name" {
  value       = aws_lambda_function.this.function_name
  description = "Lambda function name"
}

output "function_arn" {
  value       = aws_lambda_function.this.arn
  description = "Lambda function ARN"
}

output "invoke_arn" {
  value       = aws_lambda_function.this.invoke_arn
  description = "Lambda invoke ARN (for API Gateway integration)"
}

output "role_arn" {
  value       = aws_iam_role.lambda_exec.arn
  description = "Lambda execution role ARN"
}

output "security_group_id" {
  value       = local.create_default_sg ? aws_security_group.lambda[0].id : null
  description = "Default Lambda security group ID (null when security_group_ids provided or VPC disabled)"
}

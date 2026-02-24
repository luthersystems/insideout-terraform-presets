output "role_arn" {
  description = "Inspector IAM role ARN"
  value       = aws_iam_role.inspector.arn
}

output "role_name" {
  description = "Inspector IAM role name"
  value       = aws_iam_role.inspector.name
}

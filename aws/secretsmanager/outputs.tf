output "secret_ids" {
  value = aws_secretsmanager_secret.secrets[*].id
}

output "secret_arns" {
  value = aws_secretsmanager_secret.secrets[*].arn
}

output "cluster_arn" {
  description = "MSK cluster ARN"
  value       = aws_msk_cluster.this.arn
}

output "bootstrap_brokers_tls" {
  description = "TLS bootstrap brokers (use for clients)"
  value       = aws_msk_cluster.this.bootstrap_brokers_tls
}

output "bootstrap_brokers_plaintext" {
  description = "Plaintext bootstrap brokers (only when allow_plaintext=true)"
  value       = try(aws_msk_cluster.this.bootstrap_brokers, null)
}

output "security_group_id" {
  description = "Security group protecting brokers"
  value       = aws_security_group.msk.id
}

output "configuration_arn" {
  description = "Applied MSK configuration ARN"
  value       = aws_msk_configuration.this.arn
}

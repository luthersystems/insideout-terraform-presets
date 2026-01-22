output "primary_endpoint" {
  value       = aws_elasticache_replication_group.this.primary_endpoint_address
  description = "Primary (write) endpoint"
}

output "reader_endpoint" {
  value       = aws_elasticache_replication_group.this.reader_endpoint_address
  description = "Reader endpoint (balances across replicas)"
}

output "port" {
  value       = 6379
  description = "Redis port"
}

output "security_group_id" {
  value       = aws_security_group.redis.id
  description = "Security Group ID for Redis access"
}

output "auth_token" {
  value       = random_password.auth.result
  sensitive   = true
  description = "Auth token used when transit encryption is enabled"
}

output "replication_group_id" {
  value       = aws_elasticache_replication_group.this.id
  description = "ElastiCache replication group ID"
}

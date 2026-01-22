output "db_address" {
  value       = aws_db_instance.primary.address
  description = "Primary DB endpoint address"
}

output "db_port" {
  value       = aws_db_instance.primary.port
  description = "DB port"
}

output "db_username" {
  value       = var.username
  description = "Master username"
}

output "db_password" {
  value       = random_password.db.result
  sensitive   = true
  description = "Generated master password (store securely)"
}

output "security_group_id" {
  value       = aws_security_group.rds.id
  description = "RDS security group ID"
}

output "read_replica_addresses" {
  value       = [for r in aws_db_instance.replica : r.address]
  description = "Endpoints of read replicas"
}

output "instance_id" {
  value       = aws_db_instance.primary.id
  description = "DB instance identifier (for CloudWatch dimensions)"
}

output "instance_arn" {
  value       = aws_db_instance.primary.arn
  description = "RDS DB instance ARN (for AWS Backup selections)"
}

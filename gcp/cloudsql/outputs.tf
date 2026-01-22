output "instance_name" {
  description = "The instance name"
  value       = local.is_postgres ? module.sql_db[0].instance_name : module.sql_db_mysql[0].instance_name
}

output "instance_connection_name" {
  description = "The connection name for Cloud SQL Proxy"
  value       = local.is_postgres ? module.sql_db[0].instance_connection_name : module.sql_db_mysql[0].instance_connection_name
}

output "instance_self_link" {
  description = "The instance self link"
  value       = local.is_postgres ? module.sql_db[0].instance_self_link : module.sql_db_mysql[0].instance_self_link
}

output "private_ip_address" {
  description = "The private IP address"
  value       = local.is_postgres ? module.sql_db[0].private_ip_address : module.sql_db_mysql[0].private_ip_address
}

output "public_ip_address" {
  description = "The public IP address (if enabled)"
  value       = local.is_postgres ? module.sql_db[0].public_ip_address : module.sql_db_mysql[0].public_ip_address
}

output "database_name" {
  description = "The database name"
  value       = var.database_name
}

output "user_name" {
  description = "The database user name"
  value       = var.user_name
}

output "user_password" {
  description = "The database user password"
  value       = var.user_password != "" ? var.user_password : random_password.user_password[0].result
  sensitive   = true
}

output "instance_first_ip_address" {
  description = "The first IP address (private if enabled, else public)"
  value       = var.enable_private_ip ? (local.is_postgres ? module.sql_db[0].private_ip_address : module.sql_db_mysql[0].private_ip_address) : (local.is_postgres ? module.sql_db[0].public_ip_address : module.sql_db_mysql[0].public_ip_address)
}


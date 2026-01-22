output "vault_name" {
  description = "AWS Backup vault name"
  value       = aws_backup_vault.this.name
}

output "plan_id" {
  description = "AWS Backup plan ID"
  value       = aws_backup_plan.this.id
}

output "selections" {
  description = "Selection names by service (null = disabled)"
  value = {
    ec2Ebs   = try(values(aws_backup_selection.ec2_ebs)[0].name, null)
    rds      = try(values(aws_backup_selection.rds)[0].name, null)
    dynamodb = try(values(aws_backup_selection.dynamodb)[0].name, null)
    s3       = try(values(aws_backup_selection.s3)[0].name, null)
  }
}

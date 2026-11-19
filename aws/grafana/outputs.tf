output "workspace_id" {
  description = "ID of the Grafana workspace."
  value       = aws_grafana_workspace.main.id
}

output "workspace_arn" {
  description = "ARN of the Grafana workspace."
  value       = aws_grafana_workspace.main.arn
}

output "workspace_url" {
  description = "URL of the Grafana workspace."
  value       = "https://${aws_grafana_workspace.main.endpoint}"
}

output "role_arn" {
  description = "ARN of the role the workspace assumes to read CloudWatch."
  value       = aws_iam_role.grafana.arn
}

output "tags" {
  description = "The Project-tagged map applied to every taggable resource in this preset."
  value       = local.tags
}

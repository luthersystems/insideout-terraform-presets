output "service_arn" {
  description = "ARN of the App Runner service. Wire this into IAM policies / Route 53 alias targets / downstream resources that need exact-match ARN references."
  value       = aws_apprunner_service.main.arn
}

output "service_id" {
  description = "App Runner service ID (e.g. `<random-12-hex>`)."
  value       = aws_apprunner_service.main.service_id
}

output "service_url" {
  description = "App Runner-issued HTTPS URL the service is reachable at (e.g. `<random>.<region>.awsapprunner.com`). Stable across deploys."
  value       = aws_apprunner_service.main.service_url
}

output "service_status" {
  description = "App Runner service status (e.g. `RUNNING`, `PAUSED`, `CREATE_FAILED`). Useful for downstream conditional logic / health checks."
  value       = aws_apprunner_service.main.status
}

output "autoscaling_configuration_arn" {
  description = "ARN of the bound autoscaling configuration version. Tuning min/max/concurrency replaces this resource (create_before_destroy) so the ARN suffix changes on every revision."
  value       = aws_apprunner_auto_scaling_configuration_version.main.arn
}

output "access_role_arn" {
  description = "ARN of the App Runner ECR access role (null when image_repository_type = ECR_PUBLIC since the public registry needs no IAM)."
  value       = local.needs_access_role ? aws_iam_role.access[0].arn : null
}

output "instance_role_arn" {
  description = "ARN of the IAM role tasks assume at runtime. Caller-attached policies on this role govern what the app can call in AWS."
  value       = aws_iam_role.instance.arn
}

output "instance_role_name" {
  description = "Name of the instance IAM role. Useful for attaching additional managed-policy attachments from sibling modules."
  value       = aws_iam_role.instance.name
}

output "vpc_connector_arn" {
  description = "ARN of the App Runner VPC connector (null when enable_vpc_connector = false)."
  value       = local.needs_vpc_connector ? aws_apprunner_vpc_connector.main[0].arn : null
}

output "custom_domain_validation_records" {
  description = "DNS validation records the caller must add to their DNS provider for the custom-domain cert to issue. Empty list when custom_domain_name is null. Each entry has `name`, `type`, `value` — same shape as ACM's domain_validation_options."
  value       = length(aws_apprunner_custom_domain_association.main) == 0 ? [] : aws_apprunner_custom_domain_association.main[0].certificate_validation_records
}

output "tags" {
  description = "The Project-tagged map applied to every taggable resource in this preset. Other presets composing on top can `merge(module.aws_apprunner.tags, ...)` to inherit the same Project attribution."
  value       = local.tags
}

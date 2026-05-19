output "project_arn" {
  description = "ARN of the CodeBuild project. Wire this into IAM policies, EventBridge rules that target the project, or CodePipeline build stages that need exact-match ARN references."
  value       = aws_codebuild_project.main.arn
}

output "project_name" {
  description = "Name of the CodeBuild project (`<project>-<codebuild_project_name>`). Use this when invoking the project via the AWS CLI / SDK (`aws codebuild start-build --project-name ...`) or wiring it to a CodePipeline source."
  value       = aws_codebuild_project.main.name
}

output "service_role_arn" {
  description = "ARN of the IAM role CodeBuild assumes per build. Caller-attached managed-policy attachments on this role extend what builds can call in AWS beyond the preset's inline policy (CloudWatch Logs + ECR pull + optional S3 logs + optional VPC ENI lifecycle)."
  value       = aws_iam_role.service.arn
}

output "service_role_name" {
  description = "Name of the service IAM role. Useful for attaching additional managed-policy attachments from sibling modules (e.g. a stack that wants the build to deploy to ECS attaches `AmazonEC2ContainerRegistryPowerUser` here)."
  value       = aws_iam_role.service.name
}

output "logs_bucket_name" {
  description = "Name of the S3 bucket the preset provisioned for build logs (null when enable_s3_logs = false). The bucket is hardened with versioning + AES256 + public-access-block."
  value       = local.create_logs_bucket ? aws_s3_bucket.logs[0].bucket : null
}

output "logs_bucket_arn" {
  description = "ARN of the S3 bucket the preset provisioned for build logs (null when enable_s3_logs = false). Useful for sibling modules that need to read build logs out-of-stack."
  value       = local.create_logs_bucket ? aws_s3_bucket.logs[0].arn : null
}

output "tags" {
  description = "The Project-tagged map applied to every taggable resource in this preset. Other presets composing on top can `merge(module.aws_codebuild.tags, ...)` to inherit the same Project attribution."
  value       = local.tags
}

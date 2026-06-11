output "domain_id" {
  description = "SageMaker Studio domain ID (e.g. `d-abcdef12345`). Use this to reference the domain from downstream resources (user profiles, apps, spaces)."
  value       = aws_sagemaker_domain.studio.id
}

output "domain_arn" {
  description = "Full ARN of the SageMaker Studio domain. Useful for IAM policy resource references that need exact-match ARNs."
  value       = aws_sagemaker_domain.studio.arn
}

output "studio_url" {
  description = "SageMaker Studio launch URL. Authenticated users sign in at this URL; admins share it with Studio user profile holders."
  value       = aws_sagemaker_domain.studio.url
}

output "execution_role_arn" {
  description = "ARN of the IAM execution role Studio apps assume. Wire this into downstream resources that need to grant SageMaker apps access (e.g. ECR image-pull policies, downstream S3 bucket policies)."
  value       = aws_iam_role.studio_execution.arn
}

output "execution_role_name" {
  description = "Name of the IAM execution role. Useful for attaching additional managed-policy attachments from sibling modules."
  value       = aws_iam_role.studio_execution.name
}

output "workspace_bucket_name" {
  description = "S3 bucket name used as the Studio workspace. Returns the preset-created bucket name when `var.workspace_bucket` is null; otherwise echoes back the caller-supplied value so downstream wiring is uniform."
  value       = local.workspace_bucket_name
}

output "workspace_bucket_arn" {
  description = "ARN of the workspace S3 bucket (preset-managed or caller-supplied). Same null-safe behavior as `workspace_bucket_name`."
  value       = local.workspace_bucket_arn
}

output "studio_user_profile_arns" {
  description = "Map of user-profile name → ARN for every Studio user profile the preset provisioned. Empty map when `var.studio_users` is empty."
  value = {
    for name, profile in aws_sagemaker_user_profile.studio_user :
    name => profile.arn
  }
}

output "tags" {
  description = "The Project-tagged map applied to every taggable resource in this preset. Other presets composing on top can `merge(module.aws_sagemaker.tags, ...)` to inherit the same Project attribution."
  value       = local.tags
}

# -----------------------------------------------------------------------------
# Real-time inference endpoint outputs (#761). Null when enable_inference is
# false so downstream wiring can detect the Studio-only configuration.
# -----------------------------------------------------------------------------

output "endpoint_name" {
  description = "Name of the real-time inference endpoint (`<project>-endpoint`). Callers hit this via the SageMaker Runtime InvokeEndpoint API. Null when enable_inference is false."
  value       = local.enable_inference ? aws_sagemaker_endpoint.inference[0].name : null
}

output "endpoint_arn" {
  description = "Full ARN of the inference endpoint. Useful for IAM policy resource references (e.g. granting sagemaker:InvokeEndpoint to callers). Null when enable_inference is false."
  value       = local.enable_inference ? aws_sagemaker_endpoint.inference[0].arn : null
}

output "model_name" {
  description = "Name of the SageMaker model (`<project>-model`) hosting the servable container. Null when enable_inference is false."
  value       = local.enable_inference ? aws_sagemaker_model.inference[0].name : null
}

output "endpoint_config_name" {
  description = "Name of the endpoint configuration (`<project>-endpoint-config`) — the production-variant hosting plan the endpoint binds. Null when enable_inference is false."
  value       = local.enable_inference ? aws_sagemaker_endpoint_configuration.inference[0].name : null
}

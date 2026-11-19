output "pipeline_name" {
  description = "Name of the pipeline (`<project>-<pipeline_name>`)."
  value       = aws_codepipeline.main.name
}

output "pipeline_arn" {
  description = "ARN of the pipeline."
  value       = aws_codepipeline.main.arn
}

output "service_role_arn" {
  description = "ARN of the role CodePipeline assumes."
  value       = aws_iam_role.pipeline.arn
}

output "artifact_bucket_name" {
  description = "Name of the artifact bucket."
  value       = aws_s3_bucket.artifacts.bucket
}

output "connection_arn" {
  description = "ARN of the CodeStar connection the Source stage uses (null for an S3 source)."
  value       = local.connection_arn
}

output "connection_status" {
  description = "Status of the connection this preset created: PENDING until a person approves it in the AWS console. Null when the preset created none."
  value       = local.create_connection ? aws_codestarconnections_connection.github[0].connection_status : null
}

output "next_step" {
  description = "What a person must do before the pipeline can run, or an empty string."
  value       = local.create_connection ? "Approve the CodeStar connection ${local.pipeline_name} uses: AWS console > Developer Tools > Settings > Connections > ${aws_codestarconnections_connection.github[0].name} > Update pending connection. Then release a change in the pipeline." : ""
}

output "tags" {
  description = "The Project-tagged map applied to every taggable resource in this preset."
  value       = local.tags
}

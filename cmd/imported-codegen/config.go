package main

// WantedAWS lists the Phase 1 AWS resource types we generate Layer 1
// structs for. Add new types here to expand coverage.
var WantedAWS = []string{
	"aws_acm_certificate",
	// Drift coverage bundle 4 (#482) — 10 more cloud-control-routed AWS
	// types pushing DriftDetectable from 42% to ~51%. Each was already
	// cloud-control-enriched; adding the Layer 1 typed struct + Layer 2
	// curated policy.Map flips them to DriftDetectable.
	"aws_api_gateway_resource",
	"aws_apigatewayv2_stage",
	"aws_appautoscaling_policy",
	"aws_appautoscaling_target",
	"aws_athena_workgroup",
	"aws_backup_vault",
	// Final-2 enricher push (#482) — closes the last hand-rolled
	// AWS discoverer types that had no Layer 1 typed struct yet,
	// flipping AWS Enrichable coverage to 100%. Both are
	// association-style sub-resources (per-tag-on-ASG and
	// resource-arn × web-acl-arn binding respectively); the
	// generated structs follow the iam_role_policy_attachment shape.
	"aws_autoscaling_group",
	"aws_autoscaling_group_tag",
	"aws_bedrock_guardrail",
	"aws_bedrock_model_invocation_logging_configuration",
	// Bundle 4 (cont.) — CloudTrail.
	"aws_cloudtrail",
	// Drift coverage bundle 2 (#482) — cloud-control-routed AWS types
	// in the RDS / compute / monitoring family. Each was already
	// cloud-control-enriched; adding the Layer 1 typed struct + Layer 2
	// curated policy.Map flips them to DriftDetectable.
	"aws_cloudfront_distribution",
	"aws_cloudwatch_event_rule",
	"aws_cloudwatch_log_group",
	"aws_cloudwatch_metric_alarm",
	"aws_codebuild_project",
	// Bundle 4 (cont.) — CodeDeploy app.
	"aws_codedeploy_app",
	"aws_codepipeline",
	"aws_db_instance",
	"aws_dynamodb_contributor_insights",
	// Bundle 4 (cont.) — DynamoDB global table.
	"aws_dynamodb_global_table",
	"aws_dynamodb_table",
	"aws_ecs_cluster",
	// Bundle 4 (cont.) — EFS file system.
	"aws_efs_file_system",
	"aws_eks_cluster",
	"aws_elasticache_replication_group",
	// Bundle 4 (cont.) — Glue catalog database. Substituted for
	// aws_cognito_user_pool, which trips a codegen name collision (the
	// resource's `schema` nested block generates a Go type named
	// AWSCognitoUserPoolSchema that clashes with the resource's
	// generated `<Type>Schema` variable name).
	"aws_glue_catalog_database",
	// Drift coverage bundle 1 (#482) — high-value cloud-control-routed
	// AWS types. Each was already cloud-control-enriched but lacked a
	// Layer 1 typed struct (and thus a curated Layer 2 policy.Map), so
	// SUPPORTED_RESOURCES.md showed them as Enrichable but not
	// DriftDetectable. Adding the Layer 1 struct + Layer 2 policy file
	// is the minimal lift to flip each to DriftDetectable.
	"aws_iam_policy",
	"aws_iam_role",
	"aws_iam_role_policy_attachment",
	// `aws_instance` is the canonical TF name for EC2 instances
	// (the resource was never renamed to `aws_ec2_instance` upstream).
	"aws_instance",
	// Bundle 4 (cont.) — Kinesis Data Stream.
	"aws_kinesis_stream",
	"aws_kms_key",
	"aws_lambda_function",
	"aws_lambda_layer_version",
	"aws_lb",
	"aws_lb_listener",
	"aws_lb_target_group",
	// Bundle 2 (cont.) — managed-search / streaming / rotation types.
	"aws_msk_cluster",
	"aws_opensearch_domain",
	"aws_resourceexplorer2_index",
	"aws_resourceexplorer2_view",
	"aws_route53_zone",
	"aws_s3_bucket",
	// S3 bucket sub-resources (#482 push to 95% coverage). Each maps
	// to an SDK-only sub-resource discoverer already registered in
	// sdkOnlySubresourceTypeConfigs; the per-bucket GetBucket* SDK
	// calls produce the typed payload the new enrichers fan into
	// ImportedResource.Attrs.
	"aws_s3_bucket_lifecycle_configuration",
	"aws_s3_bucket_ownership_controls",
	"aws_s3_bucket_public_access_block",
	"aws_s3_bucket_server_side_encryption_configuration",
	"aws_s3_bucket_versioning",
	"aws_secretsmanager_secret",
	"aws_secretsmanager_secret_rotation",
	"aws_security_group",
	"aws_service_discovery_private_dns_namespace",
	"aws_sfn_state_machine",
	"aws_sns_topic",
	"aws_sqs_queue",
	"aws_subnet",
	"aws_vpc",
	"aws_vpc_endpoint",
	// Final-2 enricher push (#482), continued — wafv2_web_acl_association
	// is the second of the two hand-rolled types being closed; alphabetical
	// order placed it at the end of the AWS list.
	"aws_wafv2_web_acl_association",
}

// WantedGoogle lists the GCP resource types we generate Layer 1 structs
// for from the hashicorp/google provider.
var WantedGoogle = []string{
	"google_cloud_run_v2_service",
	"google_cloudbuild_trigger",
	"google_cloudfunctions2_function",
	"google_firestore_database",
	"google_compute_address",
	"google_compute_backend_service",
	"google_compute_firewall",
	"google_compute_forwarding_rule",
	"google_compute_global_address",
	"google_compute_global_forwarding_rule",
	"google_compute_health_check",
	"google_compute_instance",
	"google_compute_managed_ssl_certificate",
	"google_compute_network",
	"google_compute_resource_policy",
	"google_compute_router",
	"google_compute_security_policy",
	"google_compute_target_http_proxy",
	"google_compute_target_https_proxy",
	"google_compute_url_map",
	"google_container_cluster",
	"google_container_node_pool",
	"google_identity_platform_config",
	"google_identity_platform_default_supported_idp_config",
	"google_kms_crypto_key",
	"google_kms_key_ring",
	"google_logging_project_sink",
	"google_monitoring_alert_policy",
	"google_monitoring_dashboard",
	"google_monitoring_notification_channel",
	"google_project_service",
	"google_pubsub_subscription",
	"google_pubsub_topic",
	"google_redis_instance",
	"google_secret_manager_secret",
	"google_secret_manager_secret_version",
	"google_service_account",
	"google_service_networking_connection",
	"google_sql_database_instance",
	"google_sql_user",
	"google_storage_bucket",
	"google_storage_bucket_object",
	"google_vertex_ai_dataset",
	"google_vpc_access_connector",
	// IAM-binding types (#482 follow-up). Each maps to a discoverer
	// already registered in NewGCPDiscoverer.byType, but lacked a
	// Layer-1 typed struct (and thus an enricher) until now. The
	// per-service GetIamPolicy SDK calls produce the binding rows
	// the enrichers fan into ImportedResource.Attrs.
	"google_cloud_run_v2_service_iam_member",
	"google_cloudfunctions2_function_iam_member",
	"google_kms_crypto_key_iam_binding",
	"google_project_iam_member",
	"google_secret_manager_secret_iam_binding",
	"google_secret_manager_secret_iam_member",
	"google_storage_bucket_iam_member",
}

// WantedGoogleBeta lists the GCP resource types whose schema lives in
// the hashicorp/google-beta provider rather than hashicorp/google. The
// API Gateway resources are the canonical case — the GA provider exposes
// the data sources but not the resources, so the api_gateway preset
// declares `google-beta` and uses `provider = google-beta` on each
// resource. The codegen processes these types against the beta schema
// dump and the resulting registrations carry GoogleBetaProviderSource
// so the composer's imported-resource emission routes them through the
// `google-beta.imported` provider alias instead of `google.imported`.
var WantedGoogleBeta = []string{
	"google_api_gateway_api",
	"google_api_gateway_api_config",
	"google_api_gateway_gateway",
}

// AWSProviderSource is the Terraform Registry source string for the AWS
// provider. Pinned in schemas/providers.tf and persisted via the generated
// version.gen.go.
const AWSProviderSource = "registry.terraform.io/hashicorp/aws"

// GoogleProviderSource is the Terraform Registry source string for the
// Google provider.
const GoogleProviderSource = "registry.terraform.io/hashicorp/google"

// GoogleBetaProviderSource is the Terraform Registry source string for
// the Google Beta provider. A small set of GCP resource types — most
// notably the API Gateway family — exposes resources only under this
// provider.
const GoogleBetaProviderSource = "registry.terraform.io/hashicorp/google-beta"

// SchemaCodegenVersion is bumped whenever the generator's output format
// changes in a way that breaks readers of existing generated files.
// Persisted into the generated version.gen.go.
const SchemaCodegenVersion = "1"

package main

// WantedAWS lists the Phase 1 AWS resource types we generate Layer 1
// structs for. Add new types here to expand coverage.
var WantedAWS = []string{
	"aws_cloudwatch_log_group",
	"aws_dynamodb_table",
	"aws_lambda_function",
	"aws_s3_bucket",
	"aws_secretsmanager_secret",
	"aws_sqs_queue",
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
	"google_kms_crypto_key",
	"google_kms_key_ring",
	"google_logging_project_sink",
	"google_monitoring_alert_policy",
	"google_monitoring_dashboard",
	"google_monitoring_notification_channel",
	"google_pubsub_subscription",
	"google_pubsub_topic",
	"google_redis_instance",
	"google_secret_manager_secret",
	"google_service_account",
	"google_sql_database_instance",
	"google_sql_user",
	"google_storage_bucket",
	"google_vertex_ai_dataset",
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

package main

// WantedAWS lists the Phase 1 AWS resource types we generate Layer 1
// structs for. Add new types here to expand coverage.
var WantedAWS = []string{
	"aws_sqs_queue",
	"aws_dynamodb_table",
	"aws_cloudwatch_log_group",
	"aws_secretsmanager_secret",
	"aws_lambda_function",
}

// WantedGoogle lists the GCP resource types we generate Layer 1 structs
// for. Bundle 9 (#385) expanded coverage from 5 Phase-1 types to 25 so
// the composer's typed-Attrs path (imported_emit.go), cross-tier
// validator (validate_cross_tier.go), and policy lint
// (pkg/composer/imported/policy) treat them as first-class instead of
// falling through to the opaque-emit branch.
//
// API Gateway types (google_api_gateway_api, _api_config, _gateway) are
// intentionally omitted: they live in the hashicorp/google-beta
// provider, not hashicorp/google, and the codegen filter is keyed on a
// single provider source today. Adding them is a follow-up (track as
// #385's google-beta-provider note) — until then they continue
// emitting via the opaque-attr fallback in imported_emit.go and remain
// in the labelableGCP static allowlist for taggable().
var WantedGoogle = []string{
	"google_cloud_run_v2_service",
	"google_cloudbuild_trigger",
	"google_cloudfunctions2_function",
	"google_firestore_database",
	"google_compute_address",
	"google_compute_firewall",
	"google_compute_forwarding_rule",
	"google_compute_global_address",
	"google_compute_global_forwarding_rule",
	"google_compute_instance",
	"google_compute_network",
	"google_compute_router",
	"google_compute_security_policy",
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
}

// AWSProviderSource is the Terraform Registry source string for the AWS
// provider. Pinned in schemas/providers.tf and persisted via the generated
// version.gen.go.
const AWSProviderSource = "registry.terraform.io/hashicorp/aws"

// GoogleProviderSource is the Terraform Registry source string for the
// Google provider.
const GoogleProviderSource = "registry.terraform.io/hashicorp/google"

// SchemaCodegenVersion is bumped whenever the generator's output format
// changes in a way that breaks readers of existing generated files.
// Persisted into the generated version.gen.go.
const SchemaCodegenVersion = "1"

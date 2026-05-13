package imported

// Category returns the stable high-level UI category for a Terraform
// resource type. Categories are the six in the wizard mockup; they are
// stable wire format consumed by reliable's importer wizard. New types
// must be added explicitly — the golden snapshot test
// (TestCategory_GoldenSnapshot) fails on drift, and
// TestCategory_TotalOverDiscoverRegistry fails on unmapped types from
// SupportedDiscoverTypes("aws"|"gcp").
//
// Returns "" for unmapped types so the UI can fall back to the type
// literal under an "Other" bucket.
//
// The six categories are wire format — verbatim string contract:
//
//   - "Events"
//   - "Data Storage"
//   - "Network Security"
//   - "Observability"
//   - "Security"
//   - "Virtual Machines"
//
// The reliable importer wizard's DiscoveredResource.group field is read
// directly from this function via the unsupported.json + imported.json
// emitters. Renaming a category is a wire-format break — bump the
// reliable consumer in lockstep.
//
// Category is a pure function with no package-level mutable state.
func Category(terraformType string) string {
	if terraformType == "" {
		return ""
	}
	return categoryByTFType[terraformType]
}

// Categories returns a fresh copy of the canonical category mapping.
// Useful for the golden snapshot test, which iterates the entire map
// in deterministic order, and for downstream UIs that need to enumerate
// the supported set without reflecting on the function shape.
//
// Callers may mutate the returned map freely — it's a clone, not a
// reference to the package's internal state.
func Categories() map[string]string {
	out := make(map[string]string, len(categoryByTFType))
	for k, v := range categoryByTFType {
		out[k] = v
	}
	return out
}

// Stable category constants. Exported so consumers (e.g. the reliable
// wizard) can reference these by name rather than re-typing the
// literal strings — a typo'd "Network Securty" string compiles, an
// unexported constant typo doesn't.
const (
	CategoryEvents          = "Events"
	CategoryDataStorage     = "Data Storage"
	CategoryNetworkSecurity = "Network Security"
	CategoryObservability   = "Observability"
	CategorySecurity        = "Security"
	CategoryVirtualMachines = "Virtual Machines"
)

// categoryByTFType is the canonical mapping from Terraform resource
// type to UI category. Pinned by:
//
//   - testdata/category.golden via TestCategory_GoldenSnapshot
//     (re-seed with UPDATE_GOLDEN=1)
//   - TestCategory_TotalOverDiscoverRegistry, which asserts every type
//     in registry.SupportedDiscoverTypes("aws"|"gcp") has a non-empty
//     Category here. Adding a new type to the discover registry
//     without categorizing it fails CI.
//
// Keep entries sorted by key (provider grouping is implicit in the
// "aws_" vs "google_" prefix) so the diff at PR review time is
// readable. The golden file enforces the same key order.
var categoryByTFType = map[string]string{
	// --- AWS ---
	"aws_acm_certificate":                 CategorySecurity,
	"aws_apigatewayv2_api":                CategoryNetworkSecurity,
	"aws_apigatewayv2_authorizer":         CategoryNetworkSecurity,
	"aws_apigatewayv2_integration":        CategoryNetworkSecurity,
	"aws_apigatewayv2_route":              CategoryNetworkSecurity,
	"aws_apigatewayv2_stage":              CategoryNetworkSecurity,
	"aws_backup_plan":                     CategoryDataStorage,
	"aws_backup_selection":                CategoryDataStorage,
	"aws_backup_vault":                    CategoryDataStorage,
	"aws_bedrock_guardrail":               CategorySecurity,
	"aws_cloudfront_distribution":         CategoryNetworkSecurity,
	"aws_cloudwatch_dashboard":            CategoryObservability,
	"aws_cloudwatch_event_rule":           CategoryEvents,
	"aws_cloudwatch_log_group":            CategoryObservability,
	"aws_cloudwatch_metric_alarm":         CategoryObservability,
	"aws_cognito_identity_provider":       CategorySecurity,
	"aws_cognito_resource_server":         CategorySecurity,
	"aws_cognito_user_pool":               CategorySecurity,
	"aws_cognito_user_pool_client":        CategorySecurity,
	"aws_cognito_user_pool_domain":        CategorySecurity,
	"aws_db_instance":                     CategoryDataStorage,
	"aws_db_parameter_group":              CategoryDataStorage,
	"aws_db_subnet_group":                 CategoryDataStorage,
	"aws_dynamodb_table":                  CategoryDataStorage,
	"aws_ecr_repository":                  CategoryDataStorage,
	"aws_ecs_cluster":                     CategoryVirtualMachines,
	"aws_eip":                             CategoryNetworkSecurity,
	"aws_eks_cluster":                     CategoryVirtualMachines,
	"aws_eks_pod_identity_association":    CategorySecurity,
	"aws_elb":                             CategoryNetworkSecurity,
	"aws_iam_instance_profile":            CategorySecurity,
	"aws_iam_policy":                      CategorySecurity,
	"aws_iam_role":                        CategorySecurity,
	"aws_internet_gateway":                CategoryNetworkSecurity,
	"aws_kms_key":                         CategorySecurity,
	"aws_lambda_alias":                    CategoryVirtualMachines,
	"aws_lambda_event_source_mapping":     CategoryEvents,
	"aws_lambda_function":                 CategoryVirtualMachines,
	"aws_lb":                              CategoryNetworkSecurity,
	"aws_lb_listener":                     CategoryNetworkSecurity,
	"aws_lb_target_group":                 CategoryNetworkSecurity,
	"aws_nat_gateway":                     CategoryNetworkSecurity,
	"aws_network_acl":                     CategoryNetworkSecurity,
	"aws_network_interface":               CategoryNetworkSecurity,
	"aws_opensearchserverless_collection": CategoryDataStorage,
	"aws_rds_cluster":                     CategoryDataStorage,
	"aws_resourceexplorer2_index":         CategoryObservability,
	"aws_resourceexplorer2_view":          CategoryObservability,
	"aws_route53_zone":                    CategoryNetworkSecurity,
	"aws_route_table":                     CategoryNetworkSecurity,
	"aws_s3_bucket":                       CategoryDataStorage,
	"aws_secretsmanager_secret":           CategorySecurity,
	"aws_security_group":                  CategoryNetworkSecurity,
	"aws_sns_topic":                       CategoryEvents,
	"aws_sqs_queue":                       CategoryEvents,
	"aws_ssm_parameter":                   CategorySecurity,
	"aws_subnet":                          CategoryNetworkSecurity,
	"aws_vpc":                             CategoryNetworkSecurity,
	"aws_vpc_dhcp_options":                CategoryNetworkSecurity,
	"aws_vpc_endpoint":                    CategoryNetworkSecurity,
	"aws_wafv2_web_acl":                   CategorySecurity,

	// --- GCP ---
	"google_api_gateway_api":                 CategoryNetworkSecurity,
	"google_api_gateway_api_config":          CategoryNetworkSecurity,
	"google_api_gateway_gateway":             CategoryNetworkSecurity,
	"google_bigquery_dataset":                CategoryDataStorage,
	"google_cloud_run_service":               CategoryVirtualMachines,
	"google_cloud_run_v2_service":            CategoryVirtualMachines,
	"google_cloudbuild_trigger":              CategoryEvents,
	"google_cloudfunctions_function":         CategoryVirtualMachines,
	"google_cloudfunctions2_function":        CategoryVirtualMachines,
	"google_compute_address":                 CategoryNetworkSecurity,
	"google_compute_disk":                    CategoryDataStorage,
	"google_compute_firewall":                CategoryNetworkSecurity,
	"google_compute_forwarding_rule":         CategoryNetworkSecurity,
	"google_compute_global_address":          CategoryNetworkSecurity,
	"google_compute_global_forwarding_rule":  CategoryNetworkSecurity,
	"google_compute_instance":                CategoryVirtualMachines,
	"google_compute_network":                 CategoryNetworkSecurity,
	"google_compute_router":                  CategoryNetworkSecurity,
	"google_compute_security_policy":         CategoryNetworkSecurity,
	"google_compute_subnetwork":              CategoryNetworkSecurity,
	"google_compute_target_https_proxy":      CategoryNetworkSecurity,
	"google_compute_url_map":                 CategoryNetworkSecurity,
	"google_container_cluster":               CategoryVirtualMachines,
	"google_container_node_pool":             CategoryVirtualMachines,
	"google_firestore_database":              CategoryDataStorage,
	"google_identity_platform_config":        CategorySecurity,
	"google_kms_crypto_key":                  CategorySecurity,
	"google_kms_key_ring":                    CategorySecurity,
	"google_logging_project_sink":            CategoryObservability,
	"google_monitoring_alert_policy":         CategoryObservability,
	"google_monitoring_dashboard":            CategoryObservability,
	"google_monitoring_notification_channel": CategoryObservability,
	"google_pubsub_subscription":             CategoryEvents,
	"google_pubsub_topic":                    CategoryEvents,
	"google_redis_instance":                  CategoryDataStorage,
	"google_secret_manager_secret":           CategorySecurity,
	"google_service_account":                 CategorySecurity,
	"google_sql_database_instance":           CategoryDataStorage,
	"google_sql_user":                        CategorySecurity,
	"google_storage_bucket":                  CategoryDataStorage,
	"google_vertex_ai_dataset":               CategoryDataStorage,
}

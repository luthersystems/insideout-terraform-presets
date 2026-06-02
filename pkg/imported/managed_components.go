package imported

import "github.com/luthersystems/insideout-terraform-presets/pkg/composer"

// managedComponentPrimaryTFTypes maps each managed ComponentKey with a
// managed metrics/config surface to the primary Terraform resource type that
// represents the same component after reverse import.
//
// "Primary" is intentionally curated: many managed components fan out to
// several Terraform resources, but their detail panel charts/edits a single
// top-level resource by default. This map records that top-level resource so
// downstream consumers can assert managed/imported parity without inventing a
// local bridge between two presets-owned vocabularies.
var managedComponentPrimaryTFTypes = map[composer.ComponentKey]string{
	// AWS
	composer.KeyAWSACM:                  "aws_acm_certificate",
	composer.KeyAWSALB:                  "aws_lb",
	composer.KeyAWSAPIGateway:           "aws_apigateway_rest_api",
	composer.KeyAWSAppRunner:            "aws_apprunner_service",
	composer.KeyAWSBackups:              "aws_backup_vault",
	composer.KeyAWSBastion:              "aws_instance",
	composer.KeyAWSBedrock:              "aws_bedrock_guardrail",
	composer.KeyAWSCloudfront:           "aws_cloudfront_distribution",
	composer.KeyAWSCloudWatchLogs:       "aws_cloudwatch_log_group",
	composer.KeyAWSCloudWatchMonitoring: "aws_cloudwatch_metric_alarm",
	composer.KeyAWSCodePipeline:         "aws_codepipeline",
	composer.KeyAWSCognito:              "aws_cognito_user_pool",
	composer.KeyAWSDynamoDB:             "aws_dynamodb_table",
	composer.KeyAWSEC2:                  "aws_instance",
	composer.KeyAWSECS:                  "aws_ecs_service",
	composer.KeyAWSEKS:                  "aws_eks_cluster",
	composer.KeyAWSElastiCache:          "aws_elasticache_replication_group",
	composer.KeyAWSGrafana:              "aws_grafana_workspace",
	composer.KeyAWSKMS:                  "aws_kms_key",
	composer.KeyAWSLambda:               "aws_lambda_function",
	composer.KeyAWSMSK:                  "aws_msk_cluster",
	composer.KeyAWSOpenSearch:           "aws_opensearch_domain",
	composer.KeyAWSRDS:                  "aws_db_instance",
	composer.KeyAWSRoute53:              "aws_route53_zone",
	composer.KeyAWSS3:                   "aws_s3_bucket",
	composer.KeyAWSSageMaker:            "aws_sagemaker_domain",
	composer.KeyAWSSecretsManager:       "aws_secretsmanager_secret",
	composer.KeyAWSSQS:                  "aws_sqs_queue",
	composer.KeyAWSVPC:                  "aws_vpc",
	composer.KeyAWSWAF:                  "aws_wafv2_web_acl",

	// GCP
	composer.KeyGCPAPIGateway:       "google_api_gateway_gateway",
	composer.KeyGCPBastion:          "google_compute_instance",
	composer.KeyGCPCloudArmor:       "google_compute_security_policy",
	composer.KeyGCPCloudBuild:       "google_cloudbuild_trigger",
	composer.KeyGCPCloudDeploy:      "google_clouddeploy_delivery_pipeline",
	composer.KeyGCPCloudDNS:         "google_dns_managed_zone",
	composer.KeyGCPCloudFunctions:   "google_cloudfunctions2_function",
	composer.KeyGCPCloudKMS:         "google_kms_crypto_key",
	composer.KeyGCPCloudLogging:     "google_logging_project_sink",
	composer.KeyGCPCloudMonitoring:  "google_monitoring_alert_policy",
	composer.KeyGCPCloudRun:         "google_cloud_run_v2_service",
	composer.KeyGCPCloudSQL:         "google_sql_database_instance",
	composer.KeyGCPCompute:          "google_compute_instance",
	composer.KeyGCPFirestore:        "google_firestore_database",
	composer.KeyGCPGCS:              "google_storage_bucket",
	composer.KeyGCPGitHubActions:    "google_iam_workload_identity_pool",
	composer.KeyGCPGKE:              "google_container_cluster",
	composer.KeyGCPIdentityPlatform: "google_identity_platform_config",
	composer.KeyGCPLoadbalancer:     "google_compute_url_map",
	composer.KeyGCPMemorystore:      "google_redis_instance",
	composer.KeyGCPPubSub:           "google_pubsub_topic",
	composer.KeyGCPSecretManager:    "google_secret_manager_secret",
	composer.KeyGCPVPC:              "google_compute_network",
	composer.KeyGCPVertexAI:         "google_vertex_ai_dataset",
}

// PrimaryTFTypeForComponent returns the primary imported Terraform resource
// type for a managed component key.
func PrimaryTFTypeForComponent(key composer.ComponentKey) (string, bool) {
	tfType, ok := managedComponentPrimaryTFTypes[key]
	return tfType, ok
}

// ManagedComponentPrimaryTFTypes returns a copy of the managed-component to
// primary imported Terraform type map.
func ManagedComponentPrimaryTFTypes() map[composer.ComponentKey]string {
	out := make(map[composer.ComponentKey]string, len(managedComponentPrimaryTFTypes))
	for key, tfType := range managedComponentPrimaryTFTypes {
		out[key] = tfType
	}
	return out
}

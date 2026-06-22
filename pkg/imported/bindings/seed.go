package bindings

// seededTypes captures the tfTypes registered by init() below, so that
// tests which intentionally wipe the live registry (via resetForTest)
// can still assert on the seeded set. Read-only after init().
var seededTypes = []string{
	"aws_s3_bucket",
	"aws_dynamodb_table",
	"aws_lambda_function",
	"aws_sqs_queue",
	"aws_lb",
	"aws_rds_cluster",
	"aws_db_instance",
	"aws_sns_topic",
	"aws_cloudwatch_log_group",
	"aws_secretsmanager_secret",
	"google_storage_bucket",
	"google_pubsub_topic",
	"google_cloud_run_v2_service",
	"google_sql_database_instance",
	"google_redis_instance",
	"google_pubsub_subscription",
	"aws_apigateway_rest_api",
	"aws_lb_target_group",
	"aws_ecs_service",
	"aws_eks_cluster",
	"aws_kinesis_stream",
	"google_compute_instance",
	"google_container_cluster",
	"google_storage_bucket_object",
	"aws_cloudfront_distribution",
	"aws_msk_cluster",
	"aws_elasticache_replication_group",
	"aws_efs_file_system",
	"aws_opensearch_domain",
	"google_compute_backend_service",
	"google_vertex_ai_dataset",
	"google_logging_project_sink",
	"aws_vpc",
	"aws_route53_zone",
	"aws_kms_key",
	"aws_iam_role",
	"aws_cloudwatch_metric_alarm",
	"google_compute_network",
	"google_kms_crypto_key",
	"google_service_account",
	"aws_autoscaling_group",
	"aws_ecs_cluster",
	"aws_instance",
	"aws_nat_gateway",
	"aws_wafv2_web_acl",
	"google_cloudfunctions2_function",
	"google_monitoring_alert_policy",
	"google_secret_manager_secret",
	"aws_apigatewayv2_stage",
	"aws_cognito_user_pool",
	"aws_ebs_volume",
	"aws_eks_node_group",
	"aws_lb_listener",
	"aws_vpc_endpoint",
	"google_compute_forwarding_rule",
	"google_vpc_access_connector",
	"aws_acm_certificate",
	"aws_apigatewayv2_api",
	"aws_backup_vault",
	"aws_cloudwatch_event_rule",
	"aws_iam_policy",
	"google_compute_security_policy",
	"google_compute_router",
	"google_firestore_database",
	"aws_api_gateway_stage",
	"aws_bedrock_guardrail",
	"aws_cognito_user_pool_client",
	"aws_eks_fargate_profile",
	"google_compute_global_forwarding_rule",
	"google_compute_url_map",
	"google_container_node_pool",
	"google_kms_key_ring",
	"aws_backup_plan",
	"aws_lambda_function_url",
	"aws_apigatewayv2_route",
	"aws_apigatewayv2_integration",
	"google_compute_firewall",
	"google_project_service",
	"google_compute_target_https_proxy",
	"google_compute_target_http_proxy",
	"aws_security_group",
	"aws_subnet",
	"aws_internet_gateway",
	"aws_cloudfront_function",
	"aws_wafv2_web_acl_association",
	"google_compute_health_check",
	"google_api_gateway_gateway",
	"google_cloudbuild_trigger",
	"google_identity_platform_config",
	"aws_eip",
	"aws_network_interface",
	"aws_ssm_parameter",
	"aws_sns_topic_subscription",
	"aws_lambda_event_source_mapping",
	"aws_iam_user",
	"google_compute_address",
	"google_monitoring_notification_channel",
	"aws_route_table",
	"aws_network_acl",
	"aws_launch_template",
	"aws_eks_addon",
	"aws_lambda_alias",
	"aws_apigatewayv2_authorizer",
	"google_compute_global_address",
	"google_monitoring_dashboard",
	"aws_dynamodb_contributor_insights",
	"aws_cloudwatch_log_stream",
	"aws_secretsmanager_secret_rotation",
	"aws_service_discovery_private_dns_namespace",
	"aws_iam_group",
	"aws_iam_instance_profile",
	"google_secret_manager_secret_version",
	"google_compute_managed_ssl_certificate",
	"aws_api_gateway_deployment",
	"aws_db_parameter_group",
	"aws_cloudwatch_dashboard",
	"aws_iam_role_policy",
	"aws_iam_role_policy_attachment",
	"google_api_gateway_api",
	"google_api_gateway_api_config",
	"google_project_iam_member",
	"aws_s3_bucket_lifecycle_configuration",
	"aws_apigatewayv2_domain_name",
	"aws_kms_alias",
	"aws_msk_configuration",
	"aws_eks_access_entry",
	"google_compute_resource_policy",
	"google_sql_user",
	"google_storage_bucket_iam_member",
	"aws_db_subnet_group",
	"aws_elasticache_parameter_group",
	"aws_elasticache_subnet_group",
	"aws_cognito_user_pool_domain",
	"aws_cognito_identity_provider",
	"aws_key_pair",
	"google_service_networking_connection",
	"google_secret_manager_secret_iam_member",
	"aws_codebuild_project",
	"aws_codepipeline",
	"aws_glue_job",
	"aws_sfn_state_machine",
	"google_dns_managed_zone",
}

// seededBindings mirrors the registrations performed by init(). Used
// by seed_test.go to assert each entry's fields independent of the
// live registry state (which other tests mutate via resetForTest).
var seededBindings = map[string]ComponentMetricsBinding{
	"aws_s3_bucket": {
		Service:        "s3",
		Action:         "get-metrics",
		DimensionKey:   "BucketName",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"NumberOfObjects", "BucketSizeBytes"},
		ConfigReadback: &ConfigReadback{
			Service:      "s3",
			Action:       "list-buckets",
			ComponentKey: "aws_s3",
			EnvelopeKey:  "Buckets",
			MatchAttr:    "Name",
			MatchFrom:    "name",
			KeyMap:       map[string]string{"versioning": "versioning.enabled"},
		},
	},
	"aws_dynamodb_table": {
		Service:        "dynamodb",
		Action:         "get-metrics",
		DimensionKey:   "TableName",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"ConsumedReadCapacityUnits", "ConsumedWriteCapacityUnits", "UserErrors"},
	},
	"aws_lambda_function": {
		Service:        "lambda",
		Action:         "get-metrics",
		DimensionKey:   "FunctionName",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"Invocations", "Errors", "Duration", "Throttles"},
	},
	"aws_sqs_queue": {
		Service:        "sqs",
		Action:         "get-metrics",
		DimensionKey:   "QueueName",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"NumberOfMessagesSent", "NumberOfMessagesReceived", "ApproximateNumberOfMessagesVisible"},
	},
	"aws_lb": {
		Service:        "elb",
		Action:         "get-metrics",
		DimensionKey:   "LoadBalancer",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"RequestCount", "TargetResponseTime", "HTTPCode_Target_5XX_Count"},
	},
	"aws_rds_cluster": {
		Service:        "rds",
		Action:         "get-metrics",
		DimensionKey:   "DBClusterIdentifier",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"CPUUtilization", "DatabaseConnections", "FreeableMemory"},
	},
	"aws_db_instance": {
		Service:        "rds",
		Action:         "get-metrics",
		DimensionKey:   "DBInstanceIdentifier",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"CPUUtilization", "DatabaseConnections", "FreeableMemory", "FreeStorageSpace"},
	},
	"aws_sns_topic": {
		Service:        "sns",
		Action:         "get-metrics",
		DimensionKey:   "TopicName",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"NumberOfMessagesPublished", "NumberOfNotificationsDelivered", "NumberOfNotificationsFailed"},
	},
	"aws_cloudwatch_log_group": {
		Service:        "logs",
		Action:         "get-metrics",
		DimensionKey:   "LogGroupName",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"IncomingBytes", "IncomingLogEvents"},
	},
	"aws_secretsmanager_secret": {
		Service:        "secretsmanager",
		Action:         "get-metrics",
		DimensionKey:   "SecretName",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"Errors"},
	},
	"google_storage_bucket": {
		Service:        "storage",
		Action:         "timeseries-list",
		DimensionKey:   "bucket_name",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"storage.googleapis.com/storage/total_bytes", "storage.googleapis.com/storage/object_count"},
		ConfigReadback: &ConfigReadback{
			Service:      "gcs",
			Action:       "list-buckets",
			ComponentKey: "gcp_gcs",
			EnvelopeKey:  "buckets",
			MatchAttr:    "name",
			MatchFrom:    "name",
			KeyMap:       map[string]string{"versioning": "versioning.enabled"},
		},
	},
	"google_pubsub_topic": {
		Service:        "pubsub",
		Action:         "timeseries-list",
		DimensionKey:   "topic_id",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"pubsub.googleapis.com/topic/send_message_operation_count", "pubsub.googleapis.com/topic/byte_cost"},
	},
	"google_cloud_run_v2_service": {
		Service:        "run",
		Action:         "timeseries-list",
		DimensionKey:   "service_name",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"run.googleapis.com/request_count", "run.googleapis.com/request_latencies"},
	},
	"google_sql_database_instance": {
		Service:        "cloudsql",
		Action:         "timeseries-list",
		DimensionKey:   "database_id",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"cloudsql.googleapis.com/database/cpu/utilization", "cloudsql.googleapis.com/database/memory/utilization", "cloudsql.googleapis.com/database/disk/utilization"},
	},
	"google_redis_instance": {
		Service:        "redis",
		Action:         "timeseries-list",
		DimensionKey:   "instance_id",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"redis.googleapis.com/clients/connected", "redis.googleapis.com/memory/usage_ratio"},
	},
	"google_pubsub_subscription": {
		Service:        "pubsub",
		Action:         "timeseries-list",
		DimensionKey:   "subscription_id",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"pubsub.googleapis.com/subscription/num_undelivered_messages", "pubsub.googleapis.com/subscription/oldest_unacked_message_age"},
	},
	"aws_apigateway_rest_api": {
		Service:        "apigateway",
		Action:         "get-metrics",
		DimensionKey:   "ApiName",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"Count", "4XXError", "5XXError", "Latency"},
	},
	"aws_lb_target_group": {
		Service:        "elb",
		Action:         "get-metrics",
		DimensionKey:   "TargetGroup",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"HealthyHostCount", "UnHealthyHostCount", "RequestCount", "TargetResponseTime"},
	},
	"aws_ecs_service": {
		Service:        "ecs",
		Action:         "get-metrics",
		DimensionKey:   "ServiceName",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"CPUUtilization", "MemoryUtilization"},
	},
	"aws_eks_cluster": {
		Service:        "eks",
		Action:         "get-metrics",
		DimensionKey:   "ClusterName",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"cluster_failed_node_count", "node_cpu_utilization"},
	},
	"aws_kinesis_stream": {
		Service:        "kinesis",
		Action:         "get-metrics",
		DimensionKey:   "StreamName",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"IncomingRecords", "IncomingBytes", "GetRecords.IteratorAgeMilliseconds"},
	},
	"google_compute_instance": {
		Service:        "compute",
		Action:         "timeseries-list",
		DimensionKey:   "instance_id",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"compute.googleapis.com/instance/cpu/utilization", "compute.googleapis.com/instance/disk/read_bytes_count", "compute.googleapis.com/instance/network/sent_bytes_count"},
	},
	"google_container_cluster": {
		Service:        "container",
		Action:         "timeseries-list",
		DimensionKey:   "cluster_name",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"kubernetes.io/container/cpu/limit_utilization", "kubernetes.io/node/cpu/allocatable_utilization"},
	},
	"google_storage_bucket_object": {
		Service:        "storage",
		Action:         "timeseries-list",
		DimensionKey:   "bucket_name",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"storage.googleapis.com/storage/object_count"},
	},
	"aws_cloudfront_distribution": {
		Service:        "cloudfront",
		Action:         "get-metrics",
		DimensionKey:   "DistributionId",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"Requests", "BytesDownloaded", "4xxErrorRate", "5xxErrorRate"},
	},
	"aws_msk_cluster": {
		Service:        "kafka",
		Action:         "get-metrics",
		DimensionKey:   "ClusterName",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"BytesInPerSec", "BytesOutPerSec", "CpuIdle", "MemoryUsed"},
	},
	"aws_elasticache_replication_group": {
		Service:        "elasticache",
		Action:         "get-metrics",
		DimensionKey:   "ReplicationGroupId",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"CPUUtilization", "DatabaseMemoryUsagePercentage", "CurrConnections"},
	},
	"aws_efs_file_system": {
		Service:        "efs",
		Action:         "get-metrics",
		DimensionKey:   "FileSystemId",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"TotalIOBytes", "BurstCreditBalance", "PercentIOLimit"},
	},
	"aws_opensearch_domain": {
		Service:        "es",
		Action:         "get-metrics",
		DimensionKey:   "DomainName",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"ClusterStatus.green", "SearchableDocuments", "CPUUtilization", "JVMMemoryPressure"},
	},
	"google_compute_backend_service": {
		Service:        "loadbalancing",
		Action:         "timeseries-list",
		DimensionKey:   "backend_target_name",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"loadbalancing.googleapis.com/https/backend_request_count", "loadbalancing.googleapis.com/https/backend_latencies"},
	},
	"google_vertex_ai_dataset": {
		Service:        "aiplatform",
		Action:         "timeseries-list",
		DimensionKey:   "dataset_id",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"aiplatform.googleapis.com/dataset/data_count"},
	},
	"google_logging_project_sink": {
		Service:        "logging",
		Action:         "timeseries-list",
		DimensionKey:   "sink_name",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"logging.googleapis.com/exports/byte_count", "logging.googleapis.com/exports/log_entry_count"},
	},
	"aws_vpc": {
		Service:        "vpc",
		Action:         "get-metrics",
		DimensionKey:   "VpcId",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"network-bytes-in", "network-bytes-out"},
	},
	"aws_route53_zone": {
		Service:        "route53",
		Action:         "get-metrics",
		DimensionKey:   "HostedZoneId",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"DNSQueries"},
	},
	"aws_kms_key": {
		Service:        "kms",
		Action:         "get-metrics",
		DimensionKey:   "KeyId",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"SecondsUntilKeyMaterialExpiration"},
	},
	"aws_iam_role": {
		// IAM metrics are CloudTrail-only — registered so consumers can
		// route policy queries; DefaultMetrics intentionally empty.
		Service:       "iam",
		Action:        "get-metrics",
		DimensionKey:  "RoleName",
		DimensionFrom: "name",
	},
	"aws_cloudwatch_metric_alarm": {
		Service:        "cloudwatch",
		Action:         "get-metrics",
		DimensionKey:   "AlarmName",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"StateValue"},
	},
	"google_compute_network": {
		Service:        "compute",
		Action:         "timeseries-list",
		DimensionKey:   "network_name",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"networking.googleapis.com/vpc_flow/ingress_bytes_count", "networking.googleapis.com/vpc_flow/egress_bytes_count"},
	},
	"google_kms_crypto_key": {
		Service:        "cloudkms",
		Action:         "timeseries-list",
		DimensionKey:   "crypto_key_id",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"cloudkms.googleapis.com/key/sign_request_count", "cloudkms.googleapis.com/key/verify_request_count"},
	},
	"google_service_account": {
		// IAM-style: registered for routing only; DefaultMetrics
		// intentionally empty (mirrors aws_iam_role).
		Service:       "iam",
		Action:        "timeseries-list",
		DimensionKey:  "unique_id",
		DimensionFrom: "name",
	},
	"aws_autoscaling_group": {
		Service:        "autoscaling",
		Action:         "get-metrics",
		DimensionKey:   "AutoScalingGroupName",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"GroupDesiredCapacity", "GroupInServiceInstances", "GroupTotalInstances"},
	},
	"aws_ecs_cluster": {
		Service:        "ecs",
		Action:         "get-metrics",
		DimensionKey:   "ClusterName",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"CPUUtilization", "MemoryUtilization", "CPUReservation", "MemoryReservation"},
	},
	"aws_instance": {
		Service:        "ec2",
		Action:         "get-metrics",
		DimensionKey:   "InstanceId",
		DimensionFrom:  "id",
		DefaultMetrics: []string{"CPUUtilization", "NetworkIn", "NetworkOut", "StatusCheckFailed"},
	},
	"aws_nat_gateway": {
		Service:        "natgateway",
		Action:         "get-metrics",
		DimensionKey:   "NatGatewayId",
		DimensionFrom:  "id",
		DefaultMetrics: []string{"BytesInFromDestination", "BytesOutToDestination", "ErrorPortAllocation", "PacketsDropCount"},
	},
	"aws_wafv2_web_acl": {
		Service:        "wafv2",
		Action:         "get-metrics",
		DimensionKey:   "WebACL",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"AllowedRequests", "BlockedRequests", "CountedRequests"},
	},
	"google_cloudfunctions2_function": {
		Service:        "cloudfunctions",
		Action:         "timeseries-list",
		DimensionKey:   "function_name",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"cloudfunctions.googleapis.com/function/execution_count", "cloudfunctions.googleapis.com/function/execution_times", "cloudfunctions.googleapis.com/function/user_memory_bytes"},
	},
	"google_monitoring_alert_policy": {
		Service:        "monitoring",
		Action:         "timeseries-list",
		DimensionKey:   "policy_name",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"monitoring.googleapis.com/alert_policy/open_incidents_count"},
	},
	"google_secret_manager_secret": {
		Service:        "secretmanager",
		Action:         "timeseries-list",
		DimensionKey:   "secret_id",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"secretmanager.googleapis.com/secret/access_count", "secretmanager.googleapis.com/secret/version_count"},
	},
	"aws_apigatewayv2_stage": {
		Service:        "apigateway",
		Action:         "get-metrics",
		DimensionKey:   "Stage",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"Count", "4xx", "5xx", "Latency", "IntegrationLatency"},
	},
	"aws_cognito_user_pool": {
		Service:        "cognito",
		Action:         "get-metrics",
		DimensionKey:   "UserPool",
		DimensionFrom:  "id",
		DefaultMetrics: []string{"SignInSuccesses", "SignUpSuccesses", "TokenRefreshSuccesses", "FederationSuccesses"},
	},
	"aws_ebs_volume": {
		Service:        "ebs",
		Action:         "get-metrics",
		DimensionKey:   "VolumeId",
		DimensionFrom:  "id",
		DefaultMetrics: []string{"VolumeReadOps", "VolumeWriteOps", "VolumeQueueLength", "VolumeIdleTime"},
	},
	"aws_eks_node_group": {
		Service:        "eks",
		Action:         "get-metrics",
		DimensionKey:   "NodegroupName",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"node_cpu_utilization", "node_memory_utilization", "node_filesystem_utilization"},
	},
	"aws_lb_listener": {
		Service:        "elb",
		Action:         "get-metrics",
		DimensionKey:   "Listener",
		DimensionFrom:  "id",
		DefaultMetrics: []string{"RequestCount", "HTTPCode_ELB_4XX_Count", "HTTPCode_ELB_5XX_Count"},
	},
	"aws_vpc_endpoint": {
		Service:        "vpc",
		Action:         "get-metrics",
		DimensionKey:   "VpcEndpointId",
		DimensionFrom:  "id",
		DefaultMetrics: []string{"BytesProcessed", "PacketsDropped", "ActiveConnections"},
	},
	"google_compute_forwarding_rule": {
		Service:        "loadbalancing",
		Action:         "timeseries-list",
		DimensionKey:   "forwarding_rule_name",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"loadbalancing.googleapis.com/l3/internal/ingress_bytes_count", "loadbalancing.googleapis.com/l3/internal/egress_bytes_count"},
	},
	"google_vpc_access_connector": {
		Service:        "vpcaccess",
		Action:         "timeseries-list",
		DimensionKey:   "connector_name",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"vpcaccess.googleapis.com/connector/sent_bytes_count", "vpcaccess.googleapis.com/connector/received_bytes_count", "vpcaccess.googleapis.com/connector/instances"},
	},
	"aws_acm_certificate": {
		Service:        "acm",
		Action:         "get-metrics",
		DimensionKey:   "CertificateArn",
		DimensionFrom:  "id",
		DefaultMetrics: []string{"DaysToExpiry"},
	},
	"aws_apigatewayv2_api": {
		Service:        "apigateway",
		Action:         "get-metrics",
		DimensionKey:   "ApiId",
		DimensionFrom:  "id",
		DefaultMetrics: []string{"Count", "4xx", "5xx", "Latency", "IntegrationLatency"},
	},
	"aws_backup_vault": {
		Service:        "backup",
		Action:         "get-metrics",
		DimensionKey:   "BackupVaultName",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"NumberOfBackupJobsCompleted", "NumberOfBackupJobsFailed", "NumberOfBackupJobsExpired"},
	},
	"aws_cloudwatch_event_rule": {
		Service:        "events",
		Action:         "get-metrics",
		DimensionKey:   "RuleName",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"Invocations", "FailedInvocations", "TriggeredRules"},
	},
	"aws_iam_policy": {
		// IAM metrics are CloudTrail-only — registered so consumers can
		// route policy queries; DefaultMetrics intentionally empty.
		Service:       "iam",
		Action:        "get-metrics",
		DimensionKey:  "PolicyArn",
		DimensionFrom: "id",
	},
	"google_compute_security_policy": {
		Service:        "networksecurity",
		Action:         "timeseries-list",
		DimensionKey:   "policy_name",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"networksecurity.googleapis.com/https/request_count", "networksecurity.googleapis.com/https/dropped_request_count"},
	},
	"google_compute_router": {
		Service:        "compute",
		Action:         "timeseries-list",
		DimensionKey:   "router_id",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"router.googleapis.com/nat/sent_bytes_count", "router.googleapis.com/nat/received_bytes_count"},
	},
	"google_firestore_database": {
		Service:        "firestore",
		Action:         "timeseries-list",
		DimensionKey:   "database_id",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"firestore.googleapis.com/document/read_count", "firestore.googleapis.com/document/write_count", "firestore.googleapis.com/document/delete_count"},
	},
	"aws_api_gateway_stage": {
		Service:        "apigateway",
		Action:         "get-metrics",
		DimensionKey:   "Stage",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"Count", "4XXError", "5XXError", "Latency", "IntegrationLatency"},
	},
	"aws_bedrock_guardrail": {
		Service:        "bedrock",
		Action:         "get-metrics",
		DimensionKey:   "GuardrailId",
		DimensionFrom:  "id",
		DefaultMetrics: []string{"InvocationLatency", "InvocationClientErrors", "InvocationServerErrors", "InvocationThrottles"},
	},
	"aws_cognito_user_pool_client": {
		Service:        "cognito",
		Action:         "get-metrics",
		DimensionKey:   "UserPoolClient",
		DimensionFrom:  "id",
		DefaultMetrics: []string{"SignInSuccesses", "SignUpSuccesses", "TokenRefreshSuccesses"},
	},
	"aws_eks_fargate_profile": {
		Service:        "eks",
		Action:         "get-metrics",
		DimensionKey:   "FargateProfileName",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"pod_cpu_utilization", "pod_memory_utilization", "pod_network_rx_bytes", "pod_network_tx_bytes"},
	},
	"google_compute_global_forwarding_rule": {
		Service:        "loadbalancing",
		Action:         "timeseries-list",
		DimensionKey:   "forwarding_rule_name",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"loadbalancing.googleapis.com/https/request_count", "loadbalancing.googleapis.com/https/request_bytes_count", "loadbalancing.googleapis.com/https/total_latencies"},
	},
	"google_compute_url_map": {
		Service:        "loadbalancing",
		Action:         "timeseries-list",
		DimensionKey:   "url_map_name",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"loadbalancing.googleapis.com/https/request_count", "loadbalancing.googleapis.com/https/backend_latencies"},
	},
	"google_container_node_pool": {
		Service:        "container",
		Action:         "timeseries-list",
		DimensionKey:   "node_pool_name",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"kubernetes.io/node/cpu/allocatable_utilization", "kubernetes.io/node/memory/allocatable_utilization", "kubernetes.io/node/ephemeral_storage/used_bytes"},
	},
	"google_kms_key_ring": {
		Service:        "cloudkms",
		Action:         "timeseries-list",
		DimensionKey:   "key_ring_id",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"cloudkms.googleapis.com/key/sign_request_count", "cloudkms.googleapis.com/key/verify_request_count"},
	},
	"aws_backup_plan": {
		Service:        "backup",
		Action:         "get-metrics",
		DimensionKey:   "BackupPlanArn",
		DimensionFrom:  "id",
		DefaultMetrics: []string{"NumberOfBackupJobsCompleted", "NumberOfBackupJobsFailed", "NumberOfBackupJobsExpired"},
	},
	"aws_lambda_function_url": {
		Service:        "lambda",
		Action:         "get-metrics",
		DimensionKey:   "FunctionName",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"UrlRequestCount", "Url4xx", "Url5xx", "UrlRequestLatency"},
	},
	"aws_apigatewayv2_route": {
		Service:        "apigateway",
		Action:         "get-metrics",
		DimensionKey:   "ApiId",
		DimensionFrom:  "id",
		DefaultMetrics: []string{"Count", "4xx", "5xx", "Latency"},
	},
	"aws_apigatewayv2_integration": {
		Service:        "apigateway",
		Action:         "get-metrics",
		DimensionKey:   "ApiId",
		DimensionFrom:  "id",
		DefaultMetrics: []string{"IntegrationLatency", "Count", "4xx", "5xx"},
	},
	"google_compute_firewall": {
		Service:        "compute",
		Action:         "timeseries-list",
		DimensionKey:   "firewall_name",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"firewallinsights.googleapis.com/subnet/firewall_hit_count", "firewallinsights.googleapis.com/vm/firewall_hit_count"},
	},
	"google_project_service": {
		Service:        "serviceusage",
		Action:         "timeseries-list",
		DimensionKey:   "service",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"serviceruntime.googleapis.com/api/request_count", "serviceruntime.googleapis.com/api/request_latencies"},
	},
	"google_compute_target_https_proxy": {
		Service:        "loadbalancing",
		Action:         "timeseries-list",
		DimensionKey:   "target_proxy_name",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"loadbalancing.googleapis.com/https/request_count", "loadbalancing.googleapis.com/https/request_bytes_count", "loadbalancing.googleapis.com/https/total_latencies"},
	},
	"google_compute_target_http_proxy": {
		Service:        "loadbalancing",
		Action:         "timeseries-list",
		DimensionKey:   "target_proxy_name",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"loadbalancing.googleapis.com/https/request_count", "loadbalancing.googleapis.com/https/request_bytes_count"},
	},
	"aws_security_group": {
		Service:        "vpc",
		Action:         "get-metrics",
		DimensionKey:   "GroupId",
		DimensionFrom:  "id",
		DefaultMetrics: []string{"AllowedFlowsCount", "DeniedFlowsCount"},
	},
	"aws_subnet": {
		Service:        "vpc",
		Action:         "get-metrics",
		DimensionKey:   "Subnet",
		DimensionFrom:  "id",
		DefaultMetrics: []string{"BytesIn", "BytesOut", "PacketsIn", "PacketsOut"},
	},
	"aws_internet_gateway": {
		Service:        "vpc",
		Action:         "get-metrics",
		DimensionKey:   "InternetGatewayId",
		DimensionFrom:  "id",
		DefaultMetrics: []string{"BytesIn", "BytesOut", "PacketsIn", "PacketsOut"},
	},
	"aws_cloudfront_function": {
		Service:        "cloudfront",
		Action:         "get-metrics",
		DimensionKey:   "FunctionName",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"FunctionInvocations", "FunctionExecutionErrors", "FunctionValidationErrors", "FunctionComputeUtilization"},
	},
	"aws_wafv2_web_acl_association": {
		Service:        "wafv2",
		Action:         "get-metrics",
		DimensionKey:   "WebACL",
		DimensionFrom:  "id",
		DefaultMetrics: []string{"AllowedRequests", "BlockedRequests", "CountedRequests"},
	},
	"google_compute_health_check": {
		Service:        "loadbalancing",
		Action:         "timeseries-list",
		DimensionKey:   "health_check_name",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"loadbalancing.googleapis.com/https/backend_request_count", "monitoring.googleapis.com/uptime_check/check_passed"},
	},
	"google_api_gateway_gateway": {
		Service:        "apigateway",
		Action:         "timeseries-list",
		DimensionKey:   "gateway_id",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"apigateway.googleapis.com/gateway/request_count", "apigateway.googleapis.com/gateway/request_latencies"},
	},
	"google_cloudbuild_trigger": {
		Service:        "cloudbuild",
		Action:         "timeseries-list",
		DimensionKey:   "trigger_id",
		DimensionFrom:  "id",
		DefaultMetrics: []string{"cloudbuild.googleapis.com/build_count", "cloudbuild.googleapis.com/build/duration"},
	},
	// google_identity_platform_config is the project-scoped singleton for
	// GCP Identity Platform — there is no per-instance dimension. The
	// managed gcp_identity_platform metric (pkg/observability
	// gcpServiceMetrics["identityplatform"]) charts the consumed-API
	// request_count series under the `service` label, so the imported
	// binding mirrors that: serviceruntime.googleapis.com/api/request_count
	// filtered on the `service` dimension (= identitytoolkit.googleapis.com).
	// DimensionFrom "service" resolves from NativeIDs["service"], which the
	// non-CAI discoverer stamps to identitytoolkit.googleapis.com. Using
	// "name" here would incorrectly filter the service label to the
	// singleton resource name ("config" / projects/<p>/config).
	"google_identity_platform_config": {
		Service:        "identityplatform",
		Action:         "timeseries-list",
		DimensionKey:   "service",
		DimensionFrom:  "service",
		DefaultMetrics: []string{"serviceruntime.googleapis.com/api/request_count", "serviceruntime.googleapis.com/api/request_latencies"},
	},
	"aws_eip": {
		Service:        "ec2",
		Action:         "get-metrics",
		DimensionKey:   "AllocationId",
		DimensionFrom:  "id",
		DefaultMetrics: []string{"NetworkIn", "NetworkOut", "NetworkPacketsIn", "NetworkPacketsOut"},
	},
	"aws_network_interface": {
		Service:        "ec2",
		Action:         "get-metrics",
		DimensionKey:   "NetworkInterfaceId",
		DimensionFrom:  "id",
		DefaultMetrics: []string{"NetworkIn", "NetworkOut", "NetworkPacketsIn", "NetworkPacketsOut"},
	},
	"aws_ssm_parameter": {
		Service:        "ssm",
		Action:         "get-metrics",
		DimensionKey:   "ParameterName",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"GetParameter", "PutParameter", "DescribeParameters"},
	},
	"aws_sns_topic_subscription": {
		Service:        "sns",
		Action:         "get-metrics",
		DimensionKey:   "TopicName",
		DimensionFrom:  "id",
		DefaultMetrics: []string{"NumberOfNotificationsDelivered", "NumberOfNotificationsFailed", "NumberOfNotificationsFilteredOut"},
	},
	"aws_lambda_event_source_mapping": {
		Service:        "lambda",
		Action:         "get-metrics",
		DimensionKey:   "EventSourceMappingArn",
		DimensionFrom:  "id",
		DefaultMetrics: []string{"PollerInvocations", "OffsetLag", "IteratorAge"},
	},
	"aws_iam_user": {
		// IAM metrics are CloudTrail-only — registered so consumers can
		// route policy queries; DefaultMetrics intentionally empty.
		Service:       "iam",
		Action:        "get-metrics",
		DimensionKey:  "UserName",
		DimensionFrom: "name",
	},
	"google_compute_address": {
		Service:        "compute",
		Action:         "timeseries-list",
		DimensionKey:   "address_name",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"compute.googleapis.com/instance/network/sent_bytes_count", "compute.googleapis.com/instance/network/received_bytes_count"},
	},
	"google_monitoring_notification_channel": {
		Service:        "monitoring",
		Action:         "timeseries-list",
		DimensionKey:   "channel_id",
		DimensionFrom:  "id",
		DefaultMetrics: []string{"monitoring.googleapis.com/notification_channel/sent_count", "monitoring.googleapis.com/notification_channel/error_count"},
	},
	"aws_route_table": {
		Service:        "vpc",
		Action:         "get-metrics",
		DimensionKey:   "RouteTableId",
		DimensionFrom:  "id",
		DefaultMetrics: []string{"BytesIn", "BytesOut", "PacketsIn", "PacketsOut"},
	},
	"aws_network_acl": {
		Service:        "vpc",
		Action:         "get-metrics",
		DimensionKey:   "NetworkAclId",
		DimensionFrom:  "id",
		DefaultMetrics: []string{"AllowedFlowsCount", "DeniedFlowsCount"},
	},
	"aws_launch_template": {
		Service:        "ec2",
		Action:         "get-metrics",
		DimensionKey:   "LaunchTemplateId",
		DimensionFrom:  "id",
		DefaultMetrics: []string{"CPUUtilization", "NetworkIn", "NetworkOut", "StatusCheckFailed"},
	},
	"aws_eks_addon": {
		Service:        "eks",
		Action:         "get-metrics",
		DimensionKey:   "AddonName",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"pod_cpu_utilization", "pod_memory_utilization"},
	},
	"aws_lambda_alias": {
		Service:        "lambda",
		Action:         "get-metrics",
		DimensionKey:   "Resource",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"Invocations", "Errors", "Duration", "Throttles"},
	},
	"aws_apigatewayv2_authorizer": {
		Service:        "apigateway",
		Action:         "get-metrics",
		DimensionKey:   "ApiId",
		DimensionFrom:  "id",
		DefaultMetrics: []string{"Count", "4xx", "5xx", "Latency"},
	},
	"google_compute_global_address": {
		Service:        "compute",
		Action:         "timeseries-list",
		DimensionKey:   "address_name",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"compute.googleapis.com/instance/network/sent_bytes_count", "compute.googleapis.com/instance/network/received_bytes_count"},
	},
	"google_monitoring_dashboard": {
		Service:        "monitoring",
		Action:         "timeseries-list",
		DimensionKey:   "dashboard_id",
		DimensionFrom:  "id",
		DefaultMetrics: []string{"monitoring.googleapis.com/dashboard/view_count"},
	},
	"aws_dynamodb_contributor_insights": {
		Service:        "dynamodb",
		Action:         "get-metrics",
		DimensionKey:   "TableName",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"ConsumedReadCapacityUnits", "ConsumedWriteCapacityUnits"},
	},
	"aws_cloudwatch_log_stream": {
		Service:        "logs",
		Action:         "get-metrics",
		DimensionKey:   "LogStreamName",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"IncomingBytes", "IncomingLogEvents"},
	},
	"aws_secretsmanager_secret_rotation": {
		Service:        "secretsmanager",
		Action:         "get-metrics",
		DimensionKey:   "SecretName",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"RotationSucceeded", "RotationFailed"},
	},
	"aws_service_discovery_private_dns_namespace": {
		Service:        "servicediscovery",
		Action:         "get-metrics",
		DimensionKey:   "NamespaceId",
		DimensionFrom:  "id",
		DefaultMetrics: []string{"RegisteredInstances", "DiscoveryRequests"},
	},
	"aws_iam_group": {
		// IAM metrics are CloudTrail-only — registered so consumers can
		// route policy queries; DefaultMetrics intentionally empty.
		Service:       "iam",
		Action:        "get-metrics",
		DimensionKey:  "GroupName",
		DimensionFrom: "name",
	},
	"aws_iam_instance_profile": {
		// IAM metrics are CloudTrail-only — registered so consumers can
		// route policy queries; DefaultMetrics intentionally empty.
		Service:       "iam",
		Action:        "get-metrics",
		DimensionKey:  "InstanceProfileName",
		DimensionFrom: "name",
	},
	"google_secret_manager_secret_version": {
		Service:        "secretmanager",
		Action:         "timeseries-list",
		DimensionKey:   "secret_id",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"secretmanager.googleapis.com/secret/access_count", "secretmanager.googleapis.com/secret/version_count"},
	},
	"google_compute_managed_ssl_certificate": {
		Service:        "loadbalancing",
		Action:         "timeseries-list",
		DimensionKey:   "certificate_name",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"loadbalancing.googleapis.com/https/request_count", "loadbalancing.googleapis.com/https/backend_request_count"},
	},
	"aws_api_gateway_deployment": {
		// CloudWatch metrics for REST API deployments roll up under
		// the parent ApiName dimension; a deployment is just a
		// snapshot pointer, so consumers query Count/Latency
		// scoped to the API.
		Service:        "apigateway",
		Action:         "get-metrics",
		DimensionKey:   "ApiName",
		DimensionFrom:  "id",
		DefaultMetrics: []string{"Count", "4XXError", "5XXError", "Latency"},
	},
	"aws_db_parameter_group": {
		// Parameter groups themselves have no per-group metrics —
		// they're config attached to DB instances. Surface the
		// RDS per-instance metrics so consumers can correlate
		// param-group changes with downstream instance behavior.
		Service:        "rds",
		Action:         "get-metrics",
		DimensionKey:   "DBParameterGroupName",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"CPUUtilization", "DatabaseConnections"},
	},
	"aws_cloudwatch_dashboard": {
		Service:        "cloudwatch",
		Action:         "get-metrics",
		DimensionKey:   "DashboardName",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"GetDashboardCount", "PutDashboardCount"},
	},
	"aws_iam_role_policy": {
		// IAM metrics are CloudTrail-only — registered so consumers
		// can route policy queries; DefaultMetrics intentionally empty.
		Service:       "iam",
		Action:        "get-metrics",
		DimensionKey:  "PolicyName",
		DimensionFrom: "name",
	},
	"aws_iam_role_policy_attachment": {
		// IAM metrics are CloudTrail-only — registered so consumers
		// can route policy queries; DefaultMetrics intentionally empty.
		Service:       "iam",
		Action:        "get-metrics",
		DimensionKey:  "PolicyArn",
		DimensionFrom: "id",
	},
	"google_api_gateway_api": {
		Service:        "apigateway",
		Action:         "timeseries-list",
		DimensionKey:   "api_id",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"apigateway.googleapis.com/gateway/request_count", "apigateway.googleapis.com/gateway/request_latencies"},
	},
	"google_api_gateway_api_config": {
		Service:        "apigateway",
		Action:         "timeseries-list",
		DimensionKey:   "api_config_id",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"apigateway.googleapis.com/gateway/request_count"},
	},
	"google_project_iam_member": {
		// IAM-style: registered for routing only; DefaultMetrics
		// intentionally empty (mirrors aws_iam_role).
		Service:       "iam",
		Action:        "timeseries-list",
		DimensionKey:  "member",
		DimensionFrom: "id",
	},
	"aws_s3_bucket_lifecycle_configuration": {
		// Lifecycle config has no per-config metrics — rolls up to
		// the parent bucket's NumberOfObjects / BucketSizeBytes
		// so consumers can correlate transitions with object-count
		// changes scoped to BucketName.
		Service:        "s3",
		Action:         "get-metrics",
		DimensionKey:   "BucketName",
		DimensionFrom:  "id",
		DefaultMetrics: []string{"NumberOfObjects", "BucketSizeBytes"},
	},
	"aws_apigatewayv2_domain_name": {
		Service:        "apigateway",
		Action:         "get-metrics",
		DimensionKey:   "DomainName",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"Count", "4xx", "5xx", "Latency"},
	},
	"aws_kms_alias": {
		// Aliases are pointers to a KMS key — no per-alias metrics;
		// registered for routing only so consumers can resolve
		// alias → key and pull the underlying key's metrics.
		Service:       "kms",
		Action:        "get-metrics",
		DimensionKey:  "AliasName",
		DimensionFrom: "name",
	},
	"aws_msk_configuration": {
		// Config objects (Kafka broker config bundles) have no
		// per-config CloudWatch metrics — they're applied to clusters
		// at create-time. Registered for routing only.
		Service:       "kafka",
		Action:        "get-metrics",
		DimensionKey:  "ConfigurationArn",
		DimensionFrom: "id",
	},
	"aws_eks_access_entry": {
		// IAM-style: an EKS access entry is an RBAC binding between
		// an IAM principal and a cluster — no per-entry metrics.
		// Registered for routing only.
		Service:       "eks",
		Action:        "get-metrics",
		DimensionKey:  "PrincipalArn",
		DimensionFrom: "id",
	},
	"google_compute_resource_policy": {
		Service:        "compute",
		Action:         "timeseries-list",
		DimensionKey:   "policy_name",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"compute.googleapis.com/snapshot/total_storage_bytes", "compute.googleapis.com/snapshot/disk_size_bytes"},
	},
	"google_sql_user": {
		// IAM-style: a Cloud SQL user is a DB grant — no per-user
		// time-series. Registered for routing only.
		Service:       "cloudsql",
		Action:        "timeseries-list",
		DimensionKey:  "user_name",
		DimensionFrom: "name",
	},
	"google_storage_bucket_iam_member": {
		// IAM-style: IAM member bindings have no per-binding metrics.
		// Registered for routing only.
		Service:       "iam",
		Action:        "timeseries-list",
		DimensionKey:  "member",
		DimensionFrom: "id",
	},
	"aws_db_subnet_group": {
		// Subnet groups have no per-group CloudWatch metrics — they're
		// network plumbing attached to DB instances. Registered for
		// routing only so consumers can resolve subnet-group → owning
		// RDS instance and pull the instance's metrics.
		Service:       "rds",
		Action:        "get-metrics",
		DimensionKey:  "DBSubnetGroupName",
		DimensionFrom: "name",
	},
	"aws_elasticache_parameter_group": {
		// Parameter groups themselves have no per-group metrics —
		// they're config attached to replication groups. Registered
		// for routing only.
		Service:       "elasticache",
		Action:        "get-metrics",
		DimensionKey:  "CacheParameterGroupName",
		DimensionFrom: "name",
	},
	"aws_elasticache_subnet_group": {
		// Subnet groups have no per-group CloudWatch metrics — they're
		// network plumbing. Registered for routing only.
		Service:       "elasticache",
		Action:        "get-metrics",
		DimensionKey:  "CacheSubnetGroupName",
		DimensionFrom: "name",
	},
	"aws_cognito_user_pool_domain": {
		// Domains are routing aliases for a user pool — metrics roll up
		// to the parent UserPool dimension. Surface the pool's sign-in
		// metrics so consumers can correlate domain config with
		// authentication traffic.
		Service:        "cognito",
		Action:         "get-metrics",
		DimensionKey:   "UserPool",
		DimensionFrom:  "id",
		DefaultMetrics: []string{"SignInSuccesses", "SignInThrottles"},
	},
	"aws_cognito_identity_provider": {
		// Identity providers are federation config on a user pool;
		// CloudWatch surfaces federation activity under the parent
		// UserPool dimension.
		Service:        "cognito",
		Action:         "get-metrics",
		DimensionKey:   "UserPool",
		DimensionFrom:  "id",
		DefaultMetrics: []string{"FederationSuccesses", "FederationThrottles"},
	},
	"aws_key_pair": {
		// SSH key pairs have no per-key CloudWatch metrics — they're
		// EC2 credentials referenced by launch templates / instances.
		// Registered for routing only.
		Service:       "ec2",
		Action:        "get-metrics",
		DimensionKey:  "KeyName",
		DimensionFrom: "name",
	},
	"google_service_networking_connection": {
		Service:        "servicenetworking",
		Action:         "timeseries-list",
		DimensionKey:   "connection_name",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"networking.googleapis.com/vpc_flow/ingress_bytes_count", "networking.googleapis.com/vpc_flow/egress_bytes_count"},
	},
	"google_secret_manager_secret_iam_member": {
		// IAM-style: IAM member bindings have no per-binding metrics.
		// Registered for routing only (mirrors google_storage_bucket_iam_member).
		Service:       "iam",
		Action:        "timeseries-list",
		DimensionKey:  "member",
		DimensionFrom: "id",
	},
	// --- Codegen-only types (not in registry.SupportedDiscoverTypes; see
	// awsCodegenOnlyTypes / gcpCodegenOnlyTypes in pkg/insideout-import/registry).
	// These bindings are dormant until a per-type SDKLister / CAI mapping
	// promotes the type into the live discoverer — wired here so the
	// metrics surface lights up automatically when discovery lands. ---
	"aws_codebuild_project": {
		Service:        "codebuild",
		Action:         "get-metrics",
		DimensionKey:   "ProjectName",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"Builds", "Duration", "FailedBuilds", "SucceededBuilds"},
	},
	"aws_codepipeline": {
		Service:        "codepipeline",
		Action:         "get-metrics",
		DimensionKey:   "PipelineName",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"SucceededPipelineExecutions", "FailedPipelineExecutions", "PipelineExecutionTime"},
	},
	"aws_glue_job": {
		// CloudWatch Glue metrics are typically compound-dimensioned
		// (JobName + JobRunId + Type); JobName alone aggregates across
		// runs and metric types. Acceptable as a default surface — the
		// downstream consumer can fan out per-run / per-type if needed.
		Service:        "glue",
		Action:         "get-metrics",
		DimensionKey:   "JobName",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"glue.driver.aggregate.numCompletedTasks", "glue.driver.aggregate.numFailedTasks", "glue.driver.aggregate.elapsedTime"},
	},
	"aws_sfn_state_machine": {
		// DimensionFrom="id" because the AWS/States CloudWatch
		// dimension is StateMachineArn, and the TF resource ID for
		// aws_sfn_state_machine IS the ARN (not the name).
		Service:        "states",
		Action:         "get-metrics",
		DimensionKey:   "StateMachineArn",
		DimensionFrom:  "id",
		DefaultMetrics: []string{"ExecutionsStarted", "ExecutionsSucceeded", "ExecutionsFailed", "ExecutionTime"},
	},
	"google_dns_managed_zone": {
		Service:        "dns",
		Action:         "timeseries-list",
		DimensionKey:   "target_name",
		DimensionFrom:  "name",
		DefaultMetrics: []string{"dns.googleapis.com/query/response_count"},
	},
}

func init() {
	for _, t := range seededTypes {
		Register(t, seededBindings[t])
	}
}

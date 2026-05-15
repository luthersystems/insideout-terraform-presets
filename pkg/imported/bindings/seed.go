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
}

func init() {
	for _, t := range seededTypes {
		Register(t, seededBindings[t])
	}
}

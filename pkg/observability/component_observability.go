package observability

import (
	"sort"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer"
)

// ComponentObservability is the per-component authority record that drives
// both the alarm-author surface (per-component observability.tf in this
// repo) and the metric-watch surface (CloudWatch GetMetricData and Cloud
// Monitoring timeSeries.list).
//
// Every entry in composer.AllComponentKeys must have a record here; the
// drift-guard tests TestObservabilityCoversEveryAWSKey /
// TestObservabilityCoversEveryGCPKey fail the package build otherwise.
// Records may be empty (zero-value AWS/GCP fields) for components that
// have no metric surface (e.g. KeyAWSGitHubActions, KeyAWSCodePipeline) —
// the deferred allowlist tracks "alarms not yet authored" separately.
type ComponentObservability struct {
	// Service is the inspector-side join key (e.g. "rds", "compute") that
	// reliable's per-service discoverer dispatches on. Mirrors the value
	// in ComponentMetricsMapping[k].Service.
	Service string

	// AWS is populated for AWS-backed components and carries the
	// CloudWatch namespace / dimension / metric specs that drive both the
	// metric-fetch wrapper and the per-component alarm authoring.
	// nil for GCP components or AWS components with no metric surface.
	AWS *AWSObs

	// GCP is populated for GCP-backed components.
	GCP *GCPObs
}

// AWSObs is the AWS half of ComponentObservability. Mirrors reliable's
// serviceMetricDef one-for-one; AWSMetricSpec.Alarmed / AlarmIssue are
// net-new, used by TestObservabilitySpecMatchesEmittedAlarms (lands in
// C9) to enforce that every Alarmed=true spec has a matching
// aws_cloudwatch_metric_alarm in <module>/observability.tf.
type AWSObs struct {
	Namespace     string
	DimensionName string
	Metrics       []AWSMetricSpec
}

type AWSMetricSpec struct {
	Name       string
	Stat       string
	Label      string
	Alarmed    bool
	AlarmIssue string
}

// GCPObs is the GCP half. Mirrors reliable's gcpServiceDef.
type GCPObs struct {
	Metrics []GCPMetricSpec
}

type GCPMetricSpec struct {
	DisplayName   string
	MetricType    string
	ResourceType  string
	LabelKey      string
	Aligner       string
	GroupByLabels []string
	Alarmed       bool
	AlarmIssue    string
}

// awsServiceMetrics is the per-service catalog ported from reliable's
// metricDefinitions (internal/agentapi/aws_metrics.go:258). The
// Observability map below joins each ComponentKey to a service entry
// here via ComponentMetricsMapping. The split exists because multiple
// keys share a single service catalog (e.g. KeyAWSEC2, KeyAWSBastion,
// KeyAWSGrafana, KeyAWSCodePipeline all map to the "ec2" service).
var awsServiceMetrics = map[string]AWSObs{
	"ec2": {
		Namespace:     "AWS/EC2",
		DimensionName: "InstanceId",
		Metrics: []AWSMetricSpec{
			{Name: "CPUUtilization", Stat: "Average"},
			{Name: "NetworkIn", Stat: "Average"},
			{Name: "NetworkOut", Stat: "Average"},
			{Name: "DiskReadOps", Stat: "Sum"},
			{Name: "DiskWriteOps", Stat: "Sum"},
		},
	},
	"lambda": {
		Namespace:     "AWS/Lambda",
		DimensionName: "FunctionName",
		Metrics: []AWSMetricSpec{
			{Name: "Invocations", Stat: "Sum"},
			{Name: "Errors", Stat: "Sum"},
			{Name: "Duration", Stat: "Average"},
			{Name: "Throttles", Stat: "Sum"},
		},
	},
	"alb": {
		Namespace:     "AWS/ApplicationELB",
		DimensionName: "LoadBalancer",
		Metrics: []AWSMetricSpec{
			{Name: "RequestCount", Stat: "Sum"},
			{Name: "TargetResponseTime", Stat: "Average"},
			{Name: "HTTPCode_ELB_5XX_Count", Stat: "Sum"},
		},
	},
	"rds": {
		Namespace:     "AWS/RDS",
		DimensionName: "DBInstanceIdentifier",
		Metrics: []AWSMetricSpec{
			{Name: "CPUUtilization", Stat: "Average"},
			{Name: "FreeStorageSpace", Stat: "Average"},
			{Name: "DatabaseConnections", Stat: "Average"},
		},
	},
	"cloudfront": {
		// CacheHitRate, OriginLatency, and the per-status error rates are
		// part of the "additional CloudFront metrics" surface — AWS only
		// publishes them when aws_cloudfront_monitoring_subscription is
		// enabled on the distribution. Our preset enables the subscription
		// on every distribution (insideout-terraform-presets#96).
		Namespace:     "AWS/CloudFront",
		DimensionName: "DistributionId",
		Metrics: []AWSMetricSpec{
			{Name: "Requests", Stat: "Sum"},
			{Name: "TotalErrorRate", Stat: "Average"},
			{Name: "CacheHitRate", Stat: "Average"},
			{Name: "OriginLatency", Stat: "Average"},
			{Name: "401ErrorRate", Stat: "Average"},
			{Name: "403ErrorRate", Stat: "Average"},
			{Name: "404ErrorRate", Stat: "Average"},
			{Name: "502ErrorRate", Stat: "Average"},
			{Name: "503ErrorRate", Stat: "Average"},
			{Name: "504ErrorRate", Stat: "Average"},
		},
	},
	"apigateway": {
		// HTTP API v2 (aws_apigatewayv2_api) publishes metrics under
		// namespace AWS/ApiGateway with lowercase 4xx/5xx names — not the
		// 4XXError/5XXError names that REST API v1 uses.
		Namespace:     "AWS/ApiGateway",
		DimensionName: "ApiId",
		Metrics: []AWSMetricSpec{
			{Name: "4xx", Stat: "Sum"},
			{Name: "5xx", Stat: "Sum"},
			{Name: "Latency", Stat: "Average"},
			{Name: "Count", Stat: "Sum"},
		},
	},
	"vpc": {
		Namespace:     "AWS/NATGateway",
		DimensionName: "NatGatewayId",
		Metrics: []AWSMetricSpec{
			{Name: "BytesOutToDestination", Stat: "Sum"},
			{Name: "BytesInFromDestination", Stat: "Sum"},
		},
	},
	"s3": {
		Namespace:     "AWS/S3",
		DimensionName: "BucketName",
		Metrics: []AWSMetricSpec{
			{Name: "BucketSizeBytes", Stat: "Average"},
			{Name: "NumberOfObjects", Stat: "Average"},
		},
	},
	"sqs": {
		Namespace:     "AWS/SQS",
		DimensionName: "QueueName",
		Metrics: []AWSMetricSpec{
			{Name: "NumberOfMessagesSent", Stat: "Sum"},
			{Name: "NumberOfMessagesReceived", Stat: "Sum"},
			{Name: "ApproximateNumberOfMessagesVisible", Stat: "Average"},
			{Name: "ApproximateAgeOfOldestMessage", Stat: "Maximum"},
		},
	},
	"dynamodb": {
		Namespace:     "AWS/DynamoDB",
		DimensionName: "TableName",
		Metrics: []AWSMetricSpec{
			{Name: "ConsumedReadCapacityUnits", Stat: "Sum"},
			{Name: "ConsumedWriteCapacityUnits", Stat: "Sum"},
			{Name: "ThrottledRequests", Stat: "Sum"},
			{Name: "UserErrors", Stat: "Sum"},
		},
	},
	"cloudwatchlogs": {
		Namespace:     "AWS/Logs",
		DimensionName: "LogGroupName",
		Metrics: []AWSMetricSpec{
			{Name: "IncomingBytes", Stat: "Sum"},
			{Name: "IncomingLogEvents", Stat: "Sum"},
		},
	},
	"cognito": {
		Namespace:     "AWS/Cognito",
		DimensionName: "UserPoolId",
		Metrics: []AWSMetricSpec{
			{Name: "SignUpSuccesses", Stat: "Sum"},
			{Name: "SignInSuccesses", Stat: "Sum"},
			{Name: "TokenRefreshSuccesses", Stat: "Sum"},
		},
	},
	"opensearch": {
		Namespace:     "AWS/ES",
		DimensionName: "DomainName",
		Metrics: []AWSMetricSpec{
			{Name: "ClusterStatus.green", Stat: "Maximum"},
			{Name: "ClusterStatus.yellow", Stat: "Maximum"},
			{Name: "ClusterStatus.red", Stat: "Maximum"},
			{Name: "SearchLatency", Stat: "Average"},
			{Name: "IndexingLatency", Stat: "Average"},
			{Name: "SearchRate", Stat: "Sum"},
			{Name: "FreeStorageSpace", Stat: "Average"},
			{Name: "CPUUtilization", Stat: "Average"},
			{Name: "JVMMemoryPressure", Stat: "Average"},
		},
	},
	"bedrock": {
		Namespace:     "AWS/Bedrock",
		DimensionName: "ModelId",
		Metrics: []AWSMetricSpec{
			{Name: "Invocations", Stat: "Sum"},
			{Name: "InvocationLatency", Stat: "Average"},
			{Name: "InputTokenCount", Stat: "Sum"},
			{Name: "OutputTokenCount", Stat: "Sum"},
			{Name: "InvocationClientErrors", Stat: "Sum"},
			{Name: "InvocationServerErrors", Stat: "Sum"},
		},
	},
	"ecs": {
		Namespace:     "AWS/ECS",
		DimensionName: "ClusterName",
		Metrics: []AWSMetricSpec{
			{Name: "CPUUtilization", Stat: "Average"},
			{Name: "MemoryUtilization", Stat: "Average"},
		},
	},
	"eks": {
		// ContainerInsights — node + pod metrics published by the
		// amazon-cloudwatch-observability addon (CloudWatch agent +
		// fluent-bit DaemonSets). The aws/eks_nodegroup preset
		// installs the addon by default as of #233 Option B-1
		// (configurable via var.enable_container_insights). On
		// fresh deployments the panel populates ~5 minutes after
		// apply; clusters that opt out via the variable will
		// render "no observable resources" until they re-enable.
		//
		// Predecessors: #231 Option A pivoted onto AWS/EC2
		// InstanceId via the `eks:cluster-name` tag as a no-preset
		// fix; that path is gone now (the registry can only
		// register one namespace per service). Callers that want
		// instance-level data can still drive the dispatcher's
		// `eks list-nodes` action directly.
		Namespace:     "ContainerInsights",
		DimensionName: "ClusterName",
		Metrics: []AWSMetricSpec{
			{Name: "node_cpu_utilization", Stat: "Average"},
			{Name: "node_memory_utilization", Stat: "Average"},
			{Name: "pod_cpu_utilization", Stat: "Average"},
			{Name: "pod_memory_utilization", Stat: "Average"},
			{Name: "cluster_failed_node_count", Stat: "Maximum"},
		},
	},
	"elasticache": {
		Namespace:     "AWS/ElastiCache",
		DimensionName: "CacheClusterId",
		Metrics: []AWSMetricSpec{
			{Name: "CPUUtilization", Stat: "Average"},
			{Name: "EngineCPUUtilization", Stat: "Average"},
			{Name: "DatabaseMemoryUsagePercentage", Stat: "Average"},
			{Name: "NetworkBytesIn", Stat: "Sum"},
			{Name: "NetworkBytesOut", Stat: "Sum"},
			{Name: "CurrConnections", Stat: "Average"},
			{Name: "CacheHits", Stat: "Sum"},
			{Name: "CacheMisses", Stat: "Sum"},
		},
	},
	"msk": {
		// MSK publishes broker-health metrics only when enhanced_monitoring
		// is set to PER_BROKER. Our preset defaults to PER_BROKER as of
		// insideout-terraform-presets#102.
		Namespace:     "AWS/Kafka",
		DimensionName: "Cluster Name",
		Metrics: []AWSMetricSpec{
			{Name: "BytesInPerSec", Stat: "Sum"},
			{Name: "BytesOutPerSec", Stat: "Sum"},
			{Name: "MessagesInPerSec", Stat: "Sum"},
			{Name: "CpuUser", Stat: "Average"},
			{Name: "CpuSystem", Stat: "Average"},
			{Name: "KafkaDataLogsDiskUsed", Stat: "Average"},
			{Name: "KafkaAppLogsDiskUsed", Stat: "Average"},
			{Name: "MemoryUsed", Stat: "Average"},
			{Name: "NetworkRxDropped", Stat: "Sum"},
			{Name: "NetworkTxDropped", Stat: "Sum"},
			{Name: "NetworkProcessorAvgIdlePercent", Stat: "Average"},
			{Name: "GlobalPartitionCount", Stat: "Maximum"},
			{Name: "OfflinePartitionsCount", Stat: "Maximum"},
		},
	},
	"waf": {
		Namespace:     "AWS/WAFV2",
		DimensionName: "WebACL",
		Metrics: []AWSMetricSpec{
			{Name: "AllowedRequests", Stat: "Sum"},
			{Name: "BlockedRequests", Stat: "Sum"},
			{Name: "CountedRequests", Stat: "Sum"},
			{Name: "PassedRequests", Stat: "Sum"},
		},
	},
}

// gcpServiceMetrics is the per-service catalog ported from reliable's
// gcpMetricDefinitions (internal/agentapi/gcp_metrics.go:141).
var gcpServiceMetrics = map[string]GCPObs{
	"compute": {
		Metrics: []GCPMetricSpec{
			{MetricType: "compute.googleapis.com/instance/cpu/utilization", ResourceType: "gce_instance", LabelKey: "instance_id", Aligner: "ALIGN_MEAN", DisplayName: "CPU Utilization"},
			{MetricType: "compute.googleapis.com/instance/disk/read_ops_count", ResourceType: "gce_instance", LabelKey: "instance_id", Aligner: "ALIGN_RATE", DisplayName: "Disk Read Ops"},
			{MetricType: "compute.googleapis.com/instance/disk/write_ops_count", ResourceType: "gce_instance", LabelKey: "instance_id", Aligner: "ALIGN_RATE", DisplayName: "Disk Write Ops"},
			{MetricType: "compute.googleapis.com/instance/network/received_bytes_count", ResourceType: "gce_instance", LabelKey: "instance_id", Aligner: "ALIGN_RATE", DisplayName: "Network Received Bytes"},
			{MetricType: "compute.googleapis.com/instance/network/sent_bytes_count", ResourceType: "gce_instance", LabelKey: "instance_id", Aligner: "ALIGN_RATE", DisplayName: "Network Sent Bytes"},
		},
	},
	"cloudrun": {
		Metrics: []GCPMetricSpec{
			{MetricType: "run.googleapis.com/request_count", ResourceType: "cloud_run_revision", LabelKey: "service_name", Aligner: "ALIGN_RATE", DisplayName: "Request Count"},
			{MetricType: "run.googleapis.com/container/instance_count", ResourceType: "cloud_run_revision", LabelKey: "service_name", Aligner: "ALIGN_MEAN", DisplayName: "Instance Count"},
			{MetricType: "run.googleapis.com/request_latencies", ResourceType: "cloud_run_revision", LabelKey: "service_name", Aligner: "ALIGN_PERCENTILE_99", DisplayName: "Request Latency (p99)"},
		},
	},
	"cloudfunctions": {
		Metrics: []GCPMetricSpec{
			{MetricType: "cloudfunctions.googleapis.com/function/execution_count", ResourceType: "cloud_function", LabelKey: "function_name", Aligner: "ALIGN_RATE", GroupByLabels: []string{"status"}, DisplayName: "Execution Count (Gen1)"},
			{MetricType: "cloudfunctions.googleapis.com/function/execution_times", ResourceType: "cloud_function", LabelKey: "function_name", Aligner: "ALIGN_PERCENTILE_99", DisplayName: "Execution Time (Gen1, p99)"},
		},
	},
	"loadbalancer": {
		Metrics: []GCPMetricSpec{
			{MetricType: "loadbalancing.googleapis.com/https/request_count", ResourceType: "https_lb_rule", LabelKey: "url_map_name", Aligner: "ALIGN_RATE", GroupByLabels: []string{"response_code_class"}, DisplayName: "Request Count"},
			{MetricType: "loadbalancing.googleapis.com/https/backend_latencies", ResourceType: "https_lb_rule", LabelKey: "url_map_name", Aligner: "ALIGN_PERCENTILE_99", DisplayName: "Backend Latency (p99)"},
		},
	},
	"cloudcdn": {
		Metrics: []GCPMetricSpec{
			{MetricType: "loadbalancing.googleapis.com/https/request_count", ResourceType: "https_lb_rule", LabelKey: "url_map_name", Aligner: "ALIGN_RATE", GroupByLabels: []string{"cache_result"}, DisplayName: "Request Count"},
			{MetricType: "loadbalancing.googleapis.com/https/backend_latencies", ResourceType: "https_lb_rule", LabelKey: "url_map_name", Aligner: "ALIGN_PERCENTILE_99", DisplayName: "Backend Latency (p99)"},
			{MetricType: "loadbalancing.googleapis.com/https/backend_request_bytes_count", ResourceType: "https_lb_rule", LabelKey: "url_map_name", Aligner: "ALIGN_RATE", DisplayName: "Backend Request Bytes"},
		},
	},
	"apigateway": {
		Metrics: []GCPMetricSpec{
			{MetricType: "apigateway.googleapis.com/gateway/request_count", ResourceType: "apigateway.googleapis.com/Gateway", LabelKey: "gateway_id", Aligner: "ALIGN_RATE", GroupByLabels: []string{"response_code_class"}, DisplayName: "Request Count"},
			{MetricType: "apigateway.googleapis.com/gateway/latencies", ResourceType: "apigateway.googleapis.com/Gateway", LabelKey: "gateway_id", Aligner: "ALIGN_PERCENTILE_99", DisplayName: "Latency (p99)"},
		},
	},
	"gcs": {
		Metrics: []GCPMetricSpec{
			{MetricType: "storage.googleapis.com/storage/total_bytes", ResourceType: "gcs_bucket", LabelKey: "bucket_name", Aligner: "ALIGN_MEAN", DisplayName: "Total Bytes"},
			{MetricType: "storage.googleapis.com/storage/object_count", ResourceType: "gcs_bucket", LabelKey: "bucket_name", Aligner: "ALIGN_MEAN", DisplayName: "Object Count"},
			{MetricType: "storage.googleapis.com/api/request_count", ResourceType: "gcs_bucket", LabelKey: "bucket_name", Aligner: "ALIGN_RATE", DisplayName: "API Request Count"},
		},
	},
	"cloudsql": {
		Metrics: []GCPMetricSpec{
			{MetricType: "cloudsql.googleapis.com/database/cpu/utilization", ResourceType: "cloudsql_database", LabelKey: "database_id", Aligner: "ALIGN_MEAN", DisplayName: "CPU Utilization"},
			{MetricType: "cloudsql.googleapis.com/database/memory/utilization", ResourceType: "cloudsql_database", LabelKey: "database_id", Aligner: "ALIGN_MEAN", DisplayName: "Memory Utilization"},
			{MetricType: "cloudsql.googleapis.com/database/disk/utilization", ResourceType: "cloudsql_database", LabelKey: "database_id", Aligner: "ALIGN_MEAN", DisplayName: "Disk Utilization"},
			{MetricType: "cloudsql.googleapis.com/database/network/connections", ResourceType: "cloudsql_database", LabelKey: "database_id", Aligner: "ALIGN_MEAN", DisplayName: "Connections"},
		},
	},
	"vpc": {
		Metrics: []GCPMetricSpec{
			{MetricType: "compute.googleapis.com/firewall/dropped_packets_count", ResourceType: "gce_instance", LabelKey: "instance_id", Aligner: "ALIGN_RATE", DisplayName: "Firewall Dropped Packets"},
			{MetricType: "router.googleapis.com/nat/sent_bytes_count", ResourceType: "nat_gateway", LabelKey: "gateway_name", Aligner: "ALIGN_RATE", DisplayName: "NAT Sent Bytes"},
		},
	},
	"cloudkms": {
		Metrics: []GCPMetricSpec{
			{MetricType: "serviceruntime.googleapis.com/api/request_count", ResourceType: "consumed_api", LabelKey: "service", Aligner: "ALIGN_RATE", GroupByLabels: []string{"response_code_class"}, DisplayName: "Key Request Count"},
		},
	},
	"pubsub": {
		Metrics: []GCPMetricSpec{
			{MetricType: "pubsub.googleapis.com/topic/send_message_operation_count", ResourceType: "pubsub_topic", LabelKey: "topic_id", Aligner: "ALIGN_RATE", DisplayName: "Topic Send Message Count"},
			{MetricType: "pubsub.googleapis.com/subscription/num_undelivered_messages", ResourceType: "pubsub_subscription", LabelKey: "subscription_id", Aligner: "ALIGN_MEAN", DisplayName: "Subscription Backlog"},
			{MetricType: "pubsub.googleapis.com/subscription/oldest_unacked_message_age", ResourceType: "pubsub_subscription", LabelKey: "subscription_id", Aligner: "ALIGN_MEAN", DisplayName: "Oldest Unacked Message Age"},
		},
	},
	"firestore": {
		// Canonical resource.type for per-database firestore queries is
		// firestore.googleapis.com/Database (carries database_id /
		// location / resource_container labels). The legacy
		// firestore_instance type only carries project_id and is NOT
		// scopable per-database — so reliable's old catalog using it +
		// reliable#1259's first attempt to "fix" by setting Database
		// on document/{read,write,delete}_count both broke per-database
		// scoping in different ways.
		//
		// The truthful catalog (verified live against the Cloud
		// Monitoring MetricDescriptors API on 2026-05-02): of the four
		// metrics we want here, only `request_latencies` publishes
		// under Database. The legacy `document/{read,write,delete}_count`
		// metrics publish ONLY under firestore_instance — but their
		// modern `*_ops_count` variants publish under Database with
		// database_id, which is what we want.
		//
		// Plus a request_latencies entry so alarmedGCPMetrics[
		// KeyGCPFirestore] has a spec to flip Alarmed=true on (#204).
		Metrics: []GCPMetricSpec{
			{MetricType: "firestore.googleapis.com/api/request_latencies", ResourceType: "firestore.googleapis.com/Database", LabelKey: "database_id", Aligner: "ALIGN_PERCENTILE_99", DisplayName: "API Request Latency (p99)"},
			{MetricType: "firestore.googleapis.com/document/read_ops_count", ResourceType: "firestore.googleapis.com/Database", LabelKey: "database_id", Aligner: "ALIGN_RATE", DisplayName: "Document Read Ops"},
			{MetricType: "firestore.googleapis.com/document/write_ops_count", ResourceType: "firestore.googleapis.com/Database", LabelKey: "database_id", Aligner: "ALIGN_RATE", DisplayName: "Document Write Ops"},
			{MetricType: "firestore.googleapis.com/document/delete_ops_count", ResourceType: "firestore.googleapis.com/Database", LabelKey: "database_id", Aligner: "ALIGN_RATE", DisplayName: "Document Delete Ops"},
		},
	},
	"cloudarmor": {
		Metrics: []GCPMetricSpec{
			{MetricType: "networksecurity.googleapis.com/https/request_count", ResourceType: "https_lb_rule", LabelKey: "url_map_name", Aligner: "ALIGN_RATE", GroupByLabels: []string{"policy_name"}, DisplayName: "Cloud Armor Requests"},
		},
	},
	"memorystore": {
		Metrics: []GCPMetricSpec{
			{MetricType: "redis.googleapis.com/stats/memory/usage_ratio", ResourceType: "redis_instance", LabelKey: "instance_id", Aligner: "ALIGN_MEAN", DisplayName: "Memory Usage Ratio"},
			{MetricType: "redis.googleapis.com/clients/connected", ResourceType: "redis_instance", LabelKey: "instance_id", Aligner: "ALIGN_MEAN", DisplayName: "Connected Clients"},
			{MetricType: "redis.googleapis.com/stats/cpu_utilization", ResourceType: "redis_instance", LabelKey: "instance_id", Aligner: "ALIGN_MEAN", DisplayName: "CPU Utilization"},
		},
	},
	"cloudbuild": {
		Metrics: []GCPMetricSpec{
			{MetricType: "serviceruntime.googleapis.com/api/request_count", ResourceType: "consumed_api", LabelKey: "service", Aligner: "ALIGN_RATE", GroupByLabels: []string{"response_code_class"}, DisplayName: "Cloud Build API Requests"},
		},
	},
	"identityplatform": {
		Metrics: []GCPMetricSpec{
			{MetricType: "serviceruntime.googleapis.com/api/request_count", ResourceType: "consumed_api", LabelKey: "service", Aligner: "ALIGN_RATE", GroupByLabels: []string{"response_code_class"}, DisplayName: "Identity Platform API Requests"},
		},
	},
	"vertexai": {
		Metrics: []GCPMetricSpec{
			{MetricType: "aiplatform.googleapis.com/prediction/online/prediction_count", ResourceType: "aiplatform.googleapis.com/Endpoint", LabelKey: "endpoint_id", Aligner: "ALIGN_RATE", DisplayName: "Online Prediction Count"},
			{MetricType: "aiplatform.googleapis.com/prediction/online/error_count", ResourceType: "aiplatform.googleapis.com/Endpoint", LabelKey: "endpoint_id", Aligner: "ALIGN_RATE", DisplayName: "Online Prediction Errors"},
			{MetricType: "aiplatform.googleapis.com/prediction/online/prediction_latencies", ResourceType: "aiplatform.googleapis.com/Endpoint", LabelKey: "endpoint_id", Aligner: "ALIGN_PERCENTILE_99", DisplayName: "Online Prediction Latency (p99)"},
		},
	},
}

// awsObsFor copies an AWSObs entry by service name. Returns nil if the
// service has no metric catalog. The copy is shallow over Metrics so the
// returned record has its own slice header — callers that flip Alarmed
// per-key won't mutate the shared catalog.
func awsObsFor(service string) *AWSObs {
	def, ok := awsServiceMetrics[service]
	if !ok {
		return nil
	}
	out := def
	out.Metrics = append([]AWSMetricSpec(nil), def.Metrics...)
	return &out
}

// gcpObsFor copies a GCPObs entry by service name. Same shape as awsObsFor.
func gcpObsFor(service string) *GCPObs {
	def, ok := gcpServiceMetrics[service]
	if !ok {
		return nil
	}
	out := def
	out.Metrics = append([]GCPMetricSpec(nil), def.Metrics...)
	return &out
}

// componentObs builds a ComponentObservability record for a single key
// by joining its ComponentMetricsMapping service entry with the
// per-cloud metric catalog. Used by the Observability map initializer.
//
// After the join, any metric whose name appears in alarmedAWSMetrics[k]
// (or alarmedGCPMetrics[k]) gets its Alarmed flag flipped — that's the
// authority handshake with the per-component observability.tf files
// authored in C7/C8. TestObservabilitySpecMatchesEmittedAlarms enforces
// the HCL-side half of the contract.
func componentObs(k composer.ComponentKey) ComponentObservability {
	binding, ok := ComponentMetricsMapping[k]
	if !ok {
		return ComponentObservability{}
	}
	o := ComponentObservability{Service: binding.Service}
	if composer.CloudFor(k) == "aws" {
		o.AWS = awsObsFor(binding.Service)
		if author, ok := alarmedAWSMetrics[k]; ok && o.AWS != nil {
			set := make(map[string]bool, len(author.Metrics))
			for _, m := range author.Metrics {
				set[m] = true
			}
			for i := range o.AWS.Metrics {
				if set[o.AWS.Metrics[i].Name] {
					o.AWS.Metrics[i].Alarmed = true
				}
			}
		}
	} else {
		o.GCP = gcpObsFor(binding.Service)
		if author, ok := alarmedGCPMetrics[k]; ok && o.GCP != nil {
			set := make(map[string]bool, len(author.Metrics))
			for _, m := range author.Metrics {
				set[m] = true
			}
			for i := range o.GCP.Metrics {
				if set[o.GCP.Metrics[i].MetricType] {
					o.GCP.Metrics[i].Alarmed = true
				}
			}
		}
	}
	return o
}

// AlarmAuthor records where a key's per-component alarms live and which
// metric names from the service catalog they cover. The Module field is
// the repo-relative directory containing the observability.tf with the
// alarm resources; Metrics names match AWSMetricSpec.Name (AWS) or
// GCPMetricSpec.MetricType (GCP).
type AlarmAuthor struct {
	Module  string
	Metrics []string
}

// alarmedAWSMetrics declares which AWS metric names have a corresponding
// per-component aws_cloudwatch_metric_alarm resource in
// <Module>/observability.tf. Drives the Alarmed flag on AWS specs and
// powers TestObservabilitySpecMatchesEmittedAlarms.
//
// To add a new alarm: author the resource in <module>/observability.tf,
// then add the metric_name here (and remove the key from
// observabilityDeferred once every spec for that key is alarmed).
var alarmedAWSMetrics = map[composer.ComponentKey]AlarmAuthor{
	composer.KeyAWSALB:         {Module: "aws/alb", Metrics: []string{"HTTPCode_ELB_5XX_Count"}},
	composer.KeyAWSAPIGateway:  {Module: "aws/apigateway", Metrics: []string{"5xx"}},
	composer.KeyAWSBastion:     {Module: "aws/bastion", Metrics: []string{"CPUUtilization"}},
	composer.KeyAWSDynamoDB:    {Module: "aws/dynamodb", Metrics: []string{"ThrottledRequests"}},
	composer.KeyAWSEC2:         {Module: "aws/ec2", Metrics: []string{"CPUUtilization"}},
	composer.KeyAWSECS:         {Module: "aws/ecs", Metrics: []string{"CPUUtilization"}},
	composer.KeyAWSEKS:         {Module: "aws/eks_nodegroup", Metrics: []string{"cluster_failed_node_count"}},
	composer.KeyAWSElastiCache: {Module: "aws/elasticache", Metrics: []string{"CPUUtilization"}},
	composer.KeyAWSLambda:      {Module: "aws/lambda", Metrics: []string{"Errors"}},
	composer.KeyAWSMSK:         {Module: "aws/msk", Metrics: []string{"OfflinePartitionsCount"}},
	composer.KeyAWSOpenSearch:  {Module: "aws/opensearch", Metrics: []string{"ClusterStatus.red"}},
	composer.KeyAWSRDS:         {Module: "aws/rds", Metrics: []string{"CPUUtilization", "FreeStorageSpace"}},
	composer.KeyAWSSQS:         {Module: "aws/sqs", Metrics: []string{"ApproximateNumberOfMessagesVisible"}},
}

// alarmedGCPMetrics is the GCP analogue. Metric strings match
// GCPMetricSpec.MetricType (the "metric.type=..." filter literal in the
// per-component google_monitoring_alert_policy).
//
// Catalog gaps tracked separately: gcp/api_gateway, gcp/firestore,
// gcp/gke, gcp/bastion authored alarms whose metric.type does not
// appear in their service catalog (or whose service has no catalog
// entry at all). Those alarms exist in HCL but cannot flip a spec
// here. Reverse-drift gate is a follow-up under #204.
var alarmedGCPMetrics = map[composer.ComponentKey]AlarmAuthor{
	composer.KeyGCPAPIGateway:     {Module: "gcp/api_gateway", Metrics: []string{"apigateway.googleapis.com/gateway/request_count"}},
	composer.KeyGCPCloudFunctions: {Module: "gcp/cloud_functions", Metrics: []string{"cloudfunctions.googleapis.com/function/execution_count"}},
	composer.KeyGCPCloudRun:       {Module: "gcp/cloud_run", Metrics: []string{"run.googleapis.com/request_latencies"}},
	composer.KeyGCPCloudSQL:       {Module: "gcp/cloudsql", Metrics: []string{"cloudsql.googleapis.com/database/cpu/utilization"}},
	composer.KeyGCPCompute:        {Module: "gcp/compute", Metrics: []string{"compute.googleapis.com/instance/cpu/utilization"}},
	composer.KeyGCPFirestore:      {Module: "gcp/firestore", Metrics: []string{"firestore.googleapis.com/api/request_latencies"}},
	composer.KeyGCPLoadbalancer:   {Module: "gcp/loadbalancer", Metrics: []string{"loadbalancing.googleapis.com/https/backend_latencies"}},
	composer.KeyGCPMemorystore:    {Module: "gcp/memorystore", Metrics: []string{"redis.googleapis.com/stats/cpu_utilization"}},
	composer.KeyGCPPubSub:         {Module: "gcp/pubsub", Metrics: []string{"pubsub.googleapis.com/subscription/num_undelivered_messages"}},
}

// AlarmedAWSMetrics returns a defensive copy of the AWS authority entry
// for k, or zero-value if no alarms are authored for that key.
func AlarmedAWSMetrics(k composer.ComponentKey) AlarmAuthor {
	a, ok := alarmedAWSMetrics[k]
	if !ok {
		return AlarmAuthor{}
	}
	out := AlarmAuthor{Module: a.Module, Metrics: append([]string(nil), a.Metrics...)}
	return out
}

// AlarmedGCPMetrics returns the GCP analogue.
func AlarmedGCPMetrics(k composer.ComponentKey) AlarmAuthor {
	a, ok := alarmedGCPMetrics[k]
	if !ok {
		return AlarmAuthor{}
	}
	out := AlarmAuthor{Module: a.Module, Metrics: append([]string(nil), a.Metrics...)}
	return out
}

// Observability is the canonical authority for per-component metric
// definitions and per-component alarm authoring.
//
// During the migration window every entry is seeded with Service +
// AWS/GCP metric specs (ported from reliable's metricDefinitions /
// gcpMetricDefinitions), but Alarmed=false everywhere — alarm authoring
// lands in C7-C9 of this PR series. Components in observabilityDeferred
// have at least one Alarmed=false metric (or no metric surface at all).
//
// Every Alarmed=true spec is enforced by
// TestObservabilitySpecMatchesEmittedAlarms (lands in C9) to have a
// matching alarm resource in the module's observability.tf.
//
// Built via componentObs() so a future schema change (e.g. adding a
// Logs sub-record) only edits one place.
var Observability = func() map[composer.ComponentKey]ComponentObservability {
	out := make(map[composer.ComponentKey]ComponentObservability, len(composer.AllComponentKeys))
	for _, k := range composer.AllComponentKeys {
		out[k] = componentObs(k)
	}
	return out
}()

// observabilityDeferred carries components whose alarm authoring has
// not yet landed (or whose data table is incomplete). Each value MUST
// be a non-empty issue ref.
//
// Invariant (TestObservabilityDeferred_OnlyForUnalarmedComponents):
// every key here has at least one Alarmed=false metric, OR has no
// metric surface at all (e.g. KeyAWSGitHubActions). C9 removes a key
// from this list once every spec it owns has Alarmed=true.
var observabilityDeferred = map[composer.ComponentKey]string{
	composer.KeyAWSALB:                  "#204",
	composer.KeyAWSAPIGateway:           "#204",
	composer.KeyAWSBackups:              "#204",
	composer.KeyAWSBastion:              "#204",
	composer.KeyAWSBedrock:              "#204",
	composer.KeyAWSCloudWatchLogs:       "#204",
	composer.KeyAWSCloudWatchMonitoring: "#204",
	composer.KeyAWSCloudfront:           "#204",
	composer.KeyAWSCodePipeline:         "#204",
	composer.KeyAWSCognito:              "#204",
	composer.KeyAWSDynamoDB:             "#204",
	composer.KeyAWSEC2:                  "#204",
	composer.KeyAWSECS:                  "#204",
	composer.KeyAWSEKS:                  "#204",
	composer.KeyAWSEKSControlPlane:      "#204",
	composer.KeyAWSEKSNodeGroup:         "#204",
	composer.KeyAWSElastiCache:          "#204",
	composer.KeyAWSGitHubActions:        "#204",
	composer.KeyAWSGrafana:              "#204",
	composer.KeyAWSKMS:                  "#204",
	composer.KeyAWSLambda:               "#204",
	composer.KeyAWSMSK:                  "#204",
	composer.KeyAWSOpenSearch:           "#204",
	composer.KeyAWSRDS:                  "#204",
	composer.KeyAWSS3:                   "#204",
	composer.KeyAWSSQS:                  "#204",
	composer.KeyAWSSecretsManager:       "#204",
	composer.KeyAWSVPC:                  "#204",
	composer.KeyAWSWAF:                  "#204",
	composer.KeyGCPAPIGateway:           "#204",
	composer.KeyGCPBackups:              "#204",
	composer.KeyGCPBastion:              "#204",
	composer.KeyGCPCloudArmor:           "#204",
	composer.KeyGCPCloudBuild:           "#204",
	composer.KeyGCPCloudFunctions:       "#204",
	composer.KeyGCPCloudKMS:             "#204",
	composer.KeyGCPCloudLogging:         "#204",
	composer.KeyGCPCloudMonitoring:      "#204",
	composer.KeyGCPCloudRun:             "#204",
	composer.KeyGCPCloudSQL:             "#204",
	composer.KeyGCPCompute:              "#204",
	composer.KeyGCPFirestore:            "#204",
	composer.KeyGCPGCS:                  "#204",
	composer.KeyGCPGKE:                  "#204",
	composer.KeyGCPIdentityPlatform:     "#204",
	composer.KeyGCPLoadbalancer:         "#204",
	composer.KeyGCPMemorystore:          "#204",
	composer.KeyGCPPubSub:               "#204",
	composer.KeyGCPSecretManager:        "#204",
	composer.KeyGCPVPC:                  "#204",
	composer.KeyGCPVertexAI:             "#204",
}

// Lookup returns the ComponentObservability record for a key. Unknown
// keys return a zero-value record and false.
func Lookup(k composer.ComponentKey) (ComponentObservability, bool) {
	o, ok := Observability[k]
	return o, ok
}

// ServicesForKeys returns the deduplicated, sorted list of inspector
// service tags reachable from the given component keys. Stable order
// keeps test snapshots clean. Unknown component keys are silently
// ignored (forward-compat).
func ServicesForKeys(keys []composer.ComponentKey) []string {
	seen := make(map[string]bool, len(keys))
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		o, ok := Observability[k]
		if !ok || o.Service == "" {
			continue
		}
		if seen[o.Service] {
			continue
		}
		seen[o.Service] = true
		out = append(out, o.Service)
	}
	sort.Strings(out)
	return out
}

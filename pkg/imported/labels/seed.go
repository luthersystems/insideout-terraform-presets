package labels

// seededLabels captures the curated (label, iconKey) overrides applied
// by init() below. Kept as a package-level map so seed_test.go can
// assert on the seeded set independent of live registry mutations from
// other tests' resetForTest helper.
//
// Curation rules (read carefully before adding entries):
//
//   - Skip the default if it's already acceptable. The default rule
//     strips the cloud prefix and title-cases each underscore-delimited
//     word, so "aws_s3_bucket" → "S3 Bucket" and "google_compute_network"
//     → "Compute Network" need no override.
//   - Override when the default mangles an acronym ("Lb", "Acm",
//     "Vpc", "Iam", "Kms", "Sqs", "Sns", "Ecs", "Eks", "Rds", "Sfn",
//     "Msk", "Ebs", "Efs", "Eip", "Cloudwatch", "Cloudfront",
//     "Cloudtrail", "Cloudbuild", "Dynamodb", "Cognito", "Bedrock",
//     "Pubsub", "Wafv2", "Athena", "Vertex").
//   - Override when the icon-asset key shipped downstream uses a
//     different shorthand than the type name (e.g. `aws_lb*` icons
//     ship under the `elb` family because they predate the ALB/NLB
//     unification).
//   - Curation skips four types whose default-rule outcome is pinned
//     by cmd/imported-codegen/emit_labels_test.go's
//     TestBuildLabelsMap_KnownEntries fixture: aws_s3_bucket,
//     aws_dynamodb_table, google_pubsub_topic, google_compute_network.
//     Changing those here would require a coordinated update in the
//     codegen package, which is outside this PR's lane.
var seededLabels = map[string]entry{
	// AWS — API Gateway (v1 + v2/HTTP/WebSocket).
	"aws_api_gateway_deployment": {Label: "API Gateway Deployment", IconKey: "apigateway"},
	"aws_api_gateway_resource":   {Label: "API Gateway Resource", IconKey: "apigateway"},
	"aws_api_gateway_stage":      {Label: "API Gateway Stage", IconKey: "apigateway"},
	"aws_apigatewayv2_api":          {Label: "API Gateway v2 API", IconKey: "apigatewayv2"},
	"aws_apigatewayv2_api_mapping":  {Label: "API Gateway v2 API Mapping", IconKey: "apigatewayv2"},
	"aws_apigatewayv2_authorizer":   {Label: "API Gateway v2 Authorizer", IconKey: "apigatewayv2"},
	"aws_apigatewayv2_domain_name":  {Label: "API Gateway v2 Domain Name", IconKey: "apigatewayv2"},
	"aws_apigatewayv2_integration":  {Label: "API Gateway v2 Integration", IconKey: "apigatewayv2"},
	"aws_apigatewayv2_route":        {Label: "API Gateway v2 Route", IconKey: "apigatewayv2"},
	"aws_apigatewayv2_stage":        {Label: "API Gateway v2 Stage", IconKey: "apigatewayv2"},

	// AWS — ACM / certs.
	"aws_acm_certificate": {Label: "ACM Certificate", IconKey: "acm"},

	// AWS — CloudWatch / CloudFront / CloudTrail / CloudBuild.
	"aws_cloudwatch_log_group":              {Label: "CloudWatch Log Group", IconKey: "cloudwatch"},
	"aws_cloudwatch_log_stream":             {Label: "CloudWatch Log Stream", IconKey: "cloudwatch"},
	"aws_cloudwatch_log_resource_policy":    {Label: "CloudWatch Log Resource Policy", IconKey: "cloudwatch"},
	"aws_cloudwatch_metric_alarm":           {Label: "CloudWatch Metric Alarm", IconKey: "cloudwatch"},
	"aws_cloudwatch_dashboard":              {Label: "CloudWatch Dashboard", IconKey: "cloudwatch"},
	"aws_cloudwatch_event_bus":              {Label: "EventBridge Event Bus", IconKey: "eventbridge"},
	"aws_cloudwatch_event_rule":             {Label: "EventBridge Rule", IconKey: "eventbridge"},
	"aws_cloudfront_distribution":           {Label: "CloudFront Distribution", IconKey: "cloudfront"},
	"aws_cloudfront_function":               {Label: "CloudFront Function", IconKey: "cloudfront"},
	"aws_cloudfront_monitoring_subscription": {Label: "CloudFront Monitoring Subscription", IconKey: "cloudfront"},
	"aws_cloudfront_origin_access_identity":  {Label: "CloudFront Origin Access Identity", IconKey: "cloudfront"},
	"aws_cloudtrail":                        {Label: "CloudTrail", IconKey: "cloudtrail"},

	// AWS — DynamoDB.
	"aws_dynamodb_contributor_insights": {Label: "DynamoDB Contributor Insights", IconKey: "dynamodb"},
	"aws_dynamodb_global_table":         {Label: "DynamoDB Global Table", IconKey: "dynamodb"},

	// AWS — IAM.
	"aws_iam_group":                    {Label: "IAM Group", IconKey: "iam"},
	"aws_iam_user":                     {Label: "IAM User", IconKey: "iam"},
	"aws_iam_role":                     {Label: "IAM Role", IconKey: "iam"},
	"aws_iam_role_policy":              {Label: "IAM Role Policy", IconKey: "iam"},
	"aws_iam_role_policy_attachment":   {Label: "IAM Role Policy Attachment", IconKey: "iam"},
	"aws_iam_policy":                   {Label: "IAM Policy", IconKey: "iam"},
	"aws_iam_instance_profile":         {Label: "IAM Instance Profile", IconKey: "iam"},
	"aws_iam_service_linked_role":      {Label: "IAM Service-Linked Role", IconKey: "iam"},

	// AWS — KMS.
	"aws_kms_key":   {Label: "KMS Key", IconKey: "kms"},
	"aws_kms_alias": {Label: "KMS Alias", IconKey: "kms"},

	// AWS — VPC / networking.
	"aws_vpc":                              {Label: "VPC", IconKey: "vpc"},
	"aws_vpc_dhcp_options":                 {Label: "VPC DHCP Options", IconKey: "vpc"},
	"aws_vpc_endpoint":                     {Label: "VPC Endpoint", IconKey: "vpc"},
	"aws_vpc_security_group_egress_rule":   {Label: "VPC Security Group Egress Rule", IconKey: "vpc"},
	"aws_vpc_security_group_ingress_rule":  {Label: "VPC Security Group Ingress Rule", IconKey: "vpc"},
	"aws_nat_gateway":                      {Label: "NAT Gateway", IconKey: "vpc"},
	"aws_internet_gateway":                 {Label: "Internet Gateway", IconKey: "vpc"},
	"aws_eip":                              {Label: "Elastic IP", IconKey: "eip"},

	// AWS — Load balancers (icons ship under elb family).
	"aws_lb":              {Label: "Load Balancer (ALB/NLB)", IconKey: "elb"},
	"aws_lb_listener":     {Label: "Load Balancer Listener", IconKey: "elb"},
	"aws_lb_target_group": {Label: "Load Balancer Target Group", IconKey: "elb"},

	// AWS — Compute / EC2.
	"aws_ebs_volume":          {Label: "EBS Volume", IconKey: "ebs"},
	"aws_efs_file_system":     {Label: "EFS File System", IconKey: "efs"},

	// AWS — ECS / EKS.
	"aws_ecs_cluster":                    {Label: "ECS Cluster", IconKey: "ecs"},
	"aws_ecs_cluster_capacity_providers": {Label: "ECS Cluster Capacity Providers", IconKey: "ecs"},
	"aws_ecs_service":                    {Label: "ECS Service", IconKey: "ecs"},
	"aws_ecs_task_definition":            {Label: "ECS Task Definition", IconKey: "ecs"},
	"aws_eks_cluster":                    {Label: "EKS Cluster", IconKey: "eks"},
	"aws_eks_node_group":                 {Label: "EKS Node Group", IconKey: "eks"},
	"aws_eks_fargate_profile":            {Label: "EKS Fargate Profile", IconKey: "eks"},
	"aws_eks_addon":                      {Label: "EKS Add-On", IconKey: "eks"},
	"aws_eks_access_entry":               {Label: "EKS Access Entry", IconKey: "eks"},
	"aws_eks_pod_identity_association":   {Label: "EKS Pod Identity Association", IconKey: "eks"},

	// AWS — RDS / databases.
	"aws_rds_cluster":     {Label: "RDS Cluster", IconKey: "rds"},
	"aws_db_instance":     {Label: "RDS Instance", IconKey: "rds"},
	"aws_db_subnet_group": {Label: "RDS Subnet Group", IconKey: "rds"},
	"aws_db_parameter_group": {Label: "RDS Parameter Group", IconKey: "rds"},

	// AWS — Queues / topics / streams.
	"aws_sqs_queue":           {Label: "SQS Queue", IconKey: "sqs"},
	"aws_sns_topic":           {Label: "SNS Topic", IconKey: "sns"},
	"aws_sns_topic_subscription": {Label: "SNS Topic Subscription", IconKey: "sns"},

	// AWS — Lambda / Step Functions / MSK / OpenSearch.
	"aws_lambda_function":              {Label: "Lambda Function", IconKey: "lambda"},
	"aws_lambda_alias":                 {Label: "Lambda Alias", IconKey: "lambda"},
	"aws_lambda_function_url":          {Label: "Lambda Function URL", IconKey: "lambda"},
	"aws_lambda_event_source_mapping":  {Label: "Lambda Event Source Mapping", IconKey: "lambda"},
	"aws_lambda_layer_version":         {Label: "Lambda Layer Version", IconKey: "lambda"},
	"aws_lambda_permission":            {Label: "Lambda Permission", IconKey: "lambda"},
	"aws_sfn_state_machine":            {Label: "Step Functions State Machine", IconKey: "sfn"},
	"aws_msk_cluster":                  {Label: "MSK Cluster", IconKey: "msk"},
	"aws_msk_configuration":            {Label: "MSK Configuration", IconKey: "msk"},
	"aws_opensearch_domain":            {Label: "OpenSearch Domain", IconKey: "opensearch"},
	"aws_opensearchserverless_collection":     {Label: "OpenSearch Serverless Collection", IconKey: "opensearch"},
	"aws_opensearchserverless_access_policy":  {Label: "OpenSearch Serverless Access Policy", IconKey: "opensearch"},
	"aws_opensearchserverless_security_policy": {Label: "OpenSearch Serverless Security Policy", IconKey: "opensearch"},

	// AWS — Cognito / Bedrock / WAF.
	"aws_cognito_user_pool":            {Label: "Cognito User Pool", IconKey: "cognito"},
	"aws_cognito_user_pool_client":     {Label: "Cognito User Pool Client", IconKey: "cognito"},
	"aws_cognito_user_pool_domain":     {Label: "Cognito User Pool Domain", IconKey: "cognito"},
	"aws_cognito_identity_provider":    {Label: "Cognito Identity Provider", IconKey: "cognito"},
	"aws_cognito_resource_server":      {Label: "Cognito Resource Server", IconKey: "cognito"},
	"aws_bedrock_guardrail":            {Label: "Bedrock Guardrail", IconKey: "bedrock"},
	"aws_bedrock_model_invocation_logging_configuration": {Label: "Bedrock Model Invocation Logging Configuration", IconKey: "bedrock"},
	"aws_wafv2_web_acl":                {Label: "WAFv2 Web ACL", IconKey: "wafv2"},
	"aws_wafv2_web_acl_association":    {Label: "WAFv2 Web ACL Association", IconKey: "wafv2"},

	// AWS — Misc service families.
	"aws_athena_workgroup":             {Label: "Athena Workgroup", IconKey: "athena"},
	"aws_secretsmanager_secret":        {Label: "Secrets Manager Secret", IconKey: "secretsmanager"},
	"aws_secretsmanager_secret_rotation": {Label: "Secrets Manager Secret Rotation", IconKey: "secretsmanager"},
	"aws_ssm_parameter":                {Label: "SSM Parameter", IconKey: "ssm"},
	"aws_elasticache_replication_group": {Label: "ElastiCache Replication Group", IconKey: "elasticache"},
	"aws_elasticache_subnet_group":      {Label: "ElastiCache Subnet Group", IconKey: "elasticache"},
	"aws_elasticache_parameter_group":   {Label: "ElastiCache Parameter Group", IconKey: "elasticache"},
	"aws_resourceexplorer2_index":       {Label: "Resource Explorer Index", IconKey: "resourceexplorer"},
	"aws_resourceexplorer2_view":        {Label: "Resource Explorer View", IconKey: "resourceexplorer"},

	// GCP — Cloud Run / Cloud Functions / Cloud Build.
	"google_cloud_run_v2_service":             {Label: "Cloud Run Service", IconKey: "cloud_run"},
	"google_cloud_run_v2_service_iam_member":  {Label: "Cloud Run Service IAM Member", IconKey: "cloud_run"},
	"google_cloudfunctions2_function":         {Label: "Cloud Function (Gen 2)", IconKey: "cloud_functions"},
	"google_cloudfunctions2_function_iam_member": {Label: "Cloud Function (Gen 2) IAM Member", IconKey: "cloud_functions"},
	"google_cloudbuild_trigger":               {Label: "Cloud Build Trigger", IconKey: "cloud_build"},

	// GCP — Compute / networking.
	"google_compute_global_address":           {Label: "Compute Global Address", IconKey: "compute_address"},
	"google_compute_global_forwarding_rule":   {Label: "Compute Global Forwarding Rule", IconKey: "compute_forwarding_rule"},
	"google_compute_managed_ssl_certificate":  {Label: "Compute Managed SSL Certificate", IconKey: "compute_ssl_certificate"},
	"google_compute_target_http_proxy":        {Label: "Compute Target HTTP Proxy", IconKey: "compute_proxy"},
	"google_compute_target_https_proxy":       {Label: "Compute Target HTTPS Proxy", IconKey: "compute_proxy"},
	"google_compute_url_map":                  {Label: "Compute URL Map", IconKey: "compute_url_map"},

	// GCP — Container / GKE.
	"google_container_cluster":   {Label: "GKE Cluster", IconKey: "gke"},
	"google_container_node_pool": {Label: "GKE Node Pool", IconKey: "gke"},

	// GCP — IAM-ish.
	"google_project_iam_member":               {Label: "Project IAM Member", IconKey: "iam"},
	"google_service_account":                  {Label: "Service Account", IconKey: "iam"},
	"google_kms_crypto_key":                   {Label: "KMS Crypto Key", IconKey: "kms"},
	"google_kms_crypto_key_iam_binding":       {Label: "KMS Crypto Key IAM Binding", IconKey: "kms"},
	"google_kms_key_ring":                     {Label: "KMS Key Ring", IconKey: "kms"},
	"google_secret_manager_secret":            {Label: "Secret Manager Secret", IconKey: "secret_manager"},
	"google_secret_manager_secret_version":    {Label: "Secret Manager Secret Version", IconKey: "secret_manager"},
	"google_secret_manager_secret_iam_binding": {Label: "Secret Manager Secret IAM Binding", IconKey: "secret_manager"},
	"google_secret_manager_secret_iam_member":  {Label: "Secret Manager Secret IAM Member", IconKey: "secret_manager"},

	// GCP — Pub/Sub.
	"google_pubsub_subscription": {Label: "Pub/Sub Subscription", IconKey: "pubsub"},
	// NOTE: google_pubsub_topic intentionally not overridden — pinned
	// to default ("Pubsub Topic") by cmd/imported-codegen test fixture.

	// GCP — SQL / Firestore / Redis / Vertex.
	"google_sql_database_instance": {Label: "Cloud SQL Database Instance", IconKey: "cloud_sql"},
	"google_sql_user":              {Label: "Cloud SQL User", IconKey: "cloud_sql"},
	"google_firestore_database":    {Label: "Firestore Database", IconKey: "firestore"},
	"google_redis_instance":        {Label: "Memorystore Redis Instance", IconKey: "redis"},
	"google_vertex_ai_dataset":     {Label: "Vertex AI Dataset", IconKey: "vertex_ai"},

	// GCP — Storage / logging / monitoring / API Gateway / identity / etc.
	"google_storage_bucket":            {Label: "Storage Bucket", IconKey: "storage"},
	"google_storage_bucket_iam_member": {Label: "Storage Bucket IAM Member", IconKey: "storage"},
	"google_storage_bucket_object":     {Label: "Storage Bucket Object", IconKey: "storage"},
	"google_logging_project_sink":      {Label: "Logging Project Sink", IconKey: "logging"},
	"google_monitoring_alert_policy":   {Label: "Monitoring Alert Policy", IconKey: "monitoring"},
	"google_monitoring_dashboard":      {Label: "Monitoring Dashboard", IconKey: "monitoring"},
	"google_monitoring_notification_channel": {Label: "Monitoring Notification Channel", IconKey: "monitoring"},
	"google_api_gateway_api":                          {Label: "API Gateway API", IconKey: "api_gateway"},
	"google_api_gateway_api_config":                   {Label: "API Gateway API Config", IconKey: "api_gateway"},
	"google_api_gateway_gateway":                      {Label: "API Gateway Gateway", IconKey: "api_gateway"},
	"google_identity_platform_config":                 {Label: "Identity Platform Config", IconKey: "identity_platform"},
	"google_identity_platform_default_supported_idp_config": {Label: "Identity Platform Default Supported IdP Config", IconKey: "identity_platform"},
	"google_vpc_access_connector":     {Label: "VPC Access Connector", IconKey: "vpc"},
	"google_project_service":          {Label: "Project Service", IconKey: "project_service"},
	"google_service_networking_connection": {Label: "Service Networking Connection", IconKey: "service_networking"},
}

func init() {
	for tfType, e := range seededLabels {
		Register(tfType, e.Label, e.IconKey)
	}
}

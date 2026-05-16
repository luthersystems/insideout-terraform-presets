package main

// WantedAWS lists the Phase 1 AWS resource types we generate Layer 1
// structs for. Add new types here to expand coverage.
var WantedAWS = []string{
	"aws_acm_certificate",
	// Bundle 11 (#482) — APIGW v1 deployment. Identity is
	// (rest_api_id, id); the deployment is the snapshot the stage
	// points at. Drift on description or triggers indicates an
	// out-of-band re-deploy.
	"aws_api_gateway_deployment",
	// Drift coverage bundle 4 (#482) — 10 more cloud-control-routed AWS
	// types pushing DriftDetectable from 42% to ~51%. Each was already
	// cloud-control-enriched; adding the Layer 1 typed struct + Layer 2
	// curated policy.Map flips them to DriftDetectable.
	"aws_api_gateway_resource",
	// Bundle 7 (#482) — REST API v1 stage. Sibling to the existing
	// `aws_api_gateway_resource`; pairs with the bundle-4 v2 stage.
	"aws_api_gateway_stage",
	// Bundle 7 (#482) — APIGW v2 (HTTP API / WebSocket) sub-resources.
	// Each is the canonical wiring axis off `aws_apigatewayv2_api`:
	// authorizer (JWT / Lambda gateway), integration (backend target),
	// and route (request → integration binding).
	// Bundle 8 (#482) — APIGW v2 parent API + custom domain name. The
	// API is the top-level container; the domain name is the public
	// custom-domain endpoint that maps onto an API via api_mapping.
	"aws_apigatewayv2_api",
	// Bundle 9 (#482) — APIGW v2 API mapping. Binds a custom
	// `aws_apigatewayv2_domain_name` to a specific API + stage. Identity
	// is (domain_name, api_id, stage); api_mapping_key is the path prefix.
	"aws_apigatewayv2_api_mapping",
	"aws_apigatewayv2_authorizer",
	"aws_apigatewayv2_domain_name",
	"aws_apigatewayv2_integration",
	"aws_apigatewayv2_route",
	"aws_apigatewayv2_stage",
	"aws_appautoscaling_policy",
	"aws_appautoscaling_target",
	"aws_athena_workgroup",
	// Bundle 7 (#482) — Backup plan. Schedule + retention rules; pairs
	// with the existing `aws_backup_vault` (where snapshots land).
	"aws_backup_plan",
	// Bundle 10 (#482) — Backup selection. Identity is
	// (plan_id, name); selection_tag + resources/not_resources drive
	// which AWS resources the plan backs up. Wiring axis is iam_role_arn
	// (Backup service uses it to read resource state).
	"aws_backup_selection",
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
	// Bundle 8 (#482) — CloudFront origin access identity. Legacy OAI
	// principal used to lock S3 origins so they only serve via the
	// CloudFront distribution.
	"aws_cloudfront_origin_access_identity",
	// Bundle 10 (#482) — CloudFront function. Edge-runtime JS (viewer
	// request / response). `code` is the load-bearing payload, `runtime`
	// pins the JS engine version.
	"aws_cloudfront_function",
	// Bundle 12 (#482) — CloudFront monitoring subscription. Toggles
	// per-distribution realtime metrics. Singleton per distribution;
	// identity is the distribution id.
	"aws_cloudfront_monitoring_subscription",
	// Bundle 4 (cont.) — CloudTrail.
	"aws_cloudtrail",
	// Drift coverage bundle 2 (#482) — cloud-control-routed AWS types
	// in the RDS / compute / monitoring family. Each was already
	// cloud-control-enriched; adding the Layer 1 typed struct + Layer 2
	// curated policy.Map flips them to DriftDetectable.
	"aws_cloudfront_distribution",
	// Bundle 5 (#482) — EventBridge default event bus. The TF canonical
	// name is `aws_cloudwatch_event_bus` (the resource pre-dates the
	// EventBridge rename).
	"aws_cloudwatch_event_bus",
	"aws_cloudwatch_event_rule",
	// Bundle 10 (#482) — CloudWatch dashboard. JSON `dashboard_body` is
	// the load-bearing payload — widgets, queries, layout. Drift on the
	// body indicates out-of-band dashboard edits in the AWS console.
	"aws_cloudwatch_dashboard",
	// Bundle 11 (#482) — CloudWatch log stream. Identity is
	// (name, log_group_name). Streams are append-only and rarely change
	// after creation; drift on log_group_name indicates re-pointing.
	"aws_cloudwatch_log_stream",
	"aws_cloudwatch_log_group",
	// Bundle 12 (#482) — CloudWatch log group resource policy. JSON
	// IAM-style policy attached at the log-group scope; controls which
	// principals (typically other AWS services) can write to a group.
	// Drift on policy_document is security-critical.
	"aws_cloudwatch_log_resource_policy",
	"aws_cloudwatch_metric_alarm",
	"aws_codebuild_project",
	// Bundle 4 (cont.) — CodeDeploy app.
	"aws_codedeploy_app",
	"aws_codepipeline",
	// Bundle 9 (#482) — Cognito identity provider. Per-user-pool
	// federated IdP record (SAML / OIDC / Facebook / Google). Wires
	// (user_pool_id, provider_name).
	"aws_cognito_identity_provider",
	// Bundle 12 (#482) — Cognito resource server. Identity is
	// (identifier, user_pool_id). Defines OAuth2 scopes for an API
	// resource fronted by Cognito; drift on `scope` is a security-
	// relevant axis.
	"aws_cognito_resource_server",
	// Bundle 5 (#482) — Cognito user-pool client. The parent
	// `aws_cognito_user_pool` trips a codegen `schema`-block name
	// collision (see bundle 4); the *client* resource has no nested
	// `schema` block, so it generates cleanly.
	"aws_cognito_user_pool_client",
	// Bundle 10 (#482) — Cognito user-pool custom domain. Pins a
	// (domain, user_pool_id, certificate_arn) tuple; the domain is the
	// hosted-UI hostname end-users hit during auth.
	"aws_cognito_user_pool_domain",
	"aws_db_instance",
	// Bundle 7 (#482) — DB parameter group. Engine-family parameter set
	// applied to RDS instances (db_instance.parameter_group_name).
	"aws_db_parameter_group",
	// Bundle 6 (#482) — RDS DB subnet group (the VPC-wiring sibling to
	// the existing db_instance / rds_cluster). Cloud-control-enriched
	// already; the curated Layer 2 map adds the drift surface.
	"aws_db_subnet_group",
	"aws_dynamodb_contributor_insights",
	// Bundle 4 (cont.) — DynamoDB global table.
	"aws_dynamodb_global_table",
	"aws_dynamodb_table",
	// Bundle 7 (#482) — EBS volume. Standalone disk; instance
	// attachment is modeled separately (aws_volume_attachment).
	"aws_ebs_volume",
	"aws_ecs_cluster",
	// Bundle 10 (#482) — ECS cluster capacity providers. Identity is
	// (cluster_name); `capacity_providers` + the default-strategy block
	// drive how new services route onto Fargate / FARGATE_SPOT / EC2 ASG
	// capacity. Out-of-band changes silently retarget where new tasks
	// land.
	"aws_ecs_cluster_capacity_providers",
	// Bundle 5 (#482) — ECS service + task definition. Round out the
	// container-compute graph alongside aws_ecs_cluster.
	"aws_ecs_service",
	"aws_ecs_task_definition",
	// Bundle 4 (cont.) — EFS file system.
	"aws_efs_file_system",
	// Bundle 6 (#482) — Elastic IP. VPC-scoped allocation; instance/eni
	// association is the wiring axis.
	"aws_eip",
	// Bundle 10 (#482) — EKS managed add-on. Identity is
	// (cluster_name, addon_name); addon_version pins the version that
	// lands in the cluster, resolve_conflicts_on_* drives upgrade
	// strategy. Out-of-band addon-version flips show as drift.
	"aws_eks_addon",
	// Bundle 11 (#482) — EKS access entry. Identity is (cluster_name,
	// principal_arn). Maps an IAM principal onto the cluster's RBAC
	// access. type/kubernetes_groups/username are the drift-relevant
	// axes; out-of-band changes silently re-key cluster permissions.
	"aws_eks_access_entry",
	"aws_eks_cluster",
	// Bundle 11 (#482) — EKS Fargate profile. Identity is
	// (cluster_name, fargate_profile_name); pod_execution_role_arn +
	// subnet_ids + selectors are the wiring axes that determine which
	// pods land on Fargate.
	"aws_eks_fargate_profile",
	// Bundle 8 (#482) — EKS managed node group. Wires (cluster_name,
	// node_role_arn, subnet_ids, scaling_config); the AMI + instance
	// type set drives the workhorse compute axis.
	"aws_eks_node_group",
	// Bundle 12 (#482) — EKS pod identity association. Binds a
	// (cluster_name, namespace, service_account) tuple to an IAM role
	// arn — the post-IRSA way to grant AWS API access to pods. Drift
	// on role_arn silently re-grants privileges to the bound SA.
	"aws_eks_pod_identity_association",
	// Bundle 11 (#482) — ElastiCache parameter group. Engine-family
	// parameter set applied to ElastiCache clusters. Identity is
	// (name, family); the `parameter` block is the load-bearing surface
	// where engine tunings live.
	"aws_elasticache_parameter_group",
	"aws_elasticache_replication_group",
	// Bundle 10 (#482) — ElastiCache subnet group. VPC-wiring sibling
	// to the existing elasticache_replication_group; (name, subnet_ids)
	// is the load-bearing surface.
	"aws_elasticache_subnet_group",
	// Bundle 4 (cont.) — Glue catalog database. Substituted for
	// aws_cognito_user_pool, which trips a codegen name collision (the
	// resource's `schema` nested block generates a Go type named
	// AWSCognitoUserPoolSchema that clashes with the resource's
	// generated `<Type>Schema` variable name).
	"aws_glue_catalog_database",
	// Bundle 5 (#482) — Glue ETL job.
	"aws_glue_job",
	// Drift coverage bundle 1 (#482) — high-value cloud-control-routed
	// AWS types. Each was already cloud-control-enriched but lacked a
	// Layer 1 typed struct (and thus a curated Layer 2 policy.Map), so
	// SUPPORTED_RESOURCES.md showed them as Enrichable but not
	// DriftDetectable. Adding the Layer 1 struct + Layer 2 policy file
	// is the minimal lift to flip each to DriftDetectable.
	// Bundle 6 (#482) — IAM group + instance profile. group is the IAM
	// principal collection (memberships are separate attachment rows);
	// instance_profile binds an EC2 instance to a role.
	"aws_iam_group",
	"aws_iam_instance_profile",
	"aws_iam_policy",
	"aws_iam_role",
	// Bundle 8 (#482) — Inline role policy. Identity is
	// (role × name); the policy document is the security-critical
	// blob, hashed/compared as opaque text for drift.
	"aws_iam_role_policy",
	"aws_iam_role_policy_attachment",
	// Bundle 10 (#482) — IAM service-linked role. AWS-managed role
	// auto-created for an AWS service principal. Identity is
	// (aws_service_name, custom_suffix); description is the editable
	// surface. Drift on aws_service_name means the role was re-pointed
	// at a different service principal (effectively a different role).
	"aws_iam_service_linked_role",
	// Bundle 5 (#482) — standalone IAM user (cross-account access /
	// machine identities not modeled through roles).
	"aws_iam_user",
	// `aws_instance` is the canonical TF name for EC2 instances
	// (the resource was never renamed to `aws_ec2_instance` upstream).
	"aws_instance",
	// Bundle 6 (#482) — VPC internet gateway. Identity is (id) +
	// wiring to its attached vpc_id.
	"aws_internet_gateway",
	// Bundle 11 (#482) — EC2 key pair. SSH public key registered for
	// EC2 instance bootstrap. Identity is (key_name, key_pair_id);
	// public_key is the load-bearing key material that authorizes
	// access — drift means an out-of-band key swap.
	"aws_key_pair",
	"aws_kinesis_stream",
	// Bundle 8 (#482) — KMS alias. Identity is (name → target_key_id);
	// alias retargeting is the drift axis (rotating a workload onto a
	// different CMK without flipping ARN references at the call site).
	"aws_kms_alias",
	"aws_kms_key",
	// Bundle 5 (#482) — Lambda alias + permission. Alias drives
	// versioned-routing (CodeDeploy blue/green); permission is the
	// resource-policy statement granting invoke rights to a principal.
	"aws_lambda_alias",
	// Bundle 9 (#482) — Lambda event-source mapping. Pulls events
	// from Kinesis / DynamoDB Streams / SQS / Kafka / MSK / Amazon MQ
	// / DocumentDB into a Lambda function. Identity is (uuid,
	// event_source_arn, function_name); batching + filter knobs drive
	// the operational behavior.
	"aws_lambda_event_source_mapping",
	"aws_lambda_function",
	// Bundle 8 (#482) — Lambda function URL. The standalone HTTPS
	// endpoint pinned to a function (auth_type + cors are the
	// security-critical knobs).
	"aws_lambda_function_url",
	"aws_lambda_layer_version",
	"aws_lambda_permission",
	// Bundle 7 (#482) — Launch template. Versioned config used by ASG /
	// EC2 fleet to spin up instances (image_id, instance_type, network /
	// security-group set, user_data, block_device_mappings).
	"aws_launch_template",
	"aws_lb",
	"aws_lb_listener",
	"aws_lb_target_group",
	// Bundle 2 (cont.) — managed-search / streaming / rotation types.
	"aws_msk_cluster",
	// Bundle 5 (#482) — MSK broker-configuration revision.
	"aws_msk_configuration",
	// Bundle 6 (#482) — VPC NAT gateway. Public/private NAT wiring;
	// allocation_id (EIP), subnet_id, connectivity_type are the
	// drift-relevant axes.
	"aws_nat_gateway",
	// Bundle 9 (#482) — VPC network ACL. Stateless subnet-level firewall;
	// the ingress / egress rule set is the drift-relevant axis.
	"aws_network_acl",
	// Bundle 6 (#482) — ENI. The pluggable network attachment for EC2,
	// Lambda-in-VPC, RDS, etc. Drift-relevant axes: subnet_id, security
	// group set, private_ip_list, source_dest_check.
	"aws_network_interface",
	"aws_opensearch_domain",
	// Bundle 12 (#482) — OpenSearch Serverless collection. Identity is
	// (name, id, arn). type (SEARCH / TIMESERIES / VECTORSEARCH) +
	// kms_key_arn are the security/perf-relevant axes. standby_replicas
	// flips availability tier (ENABLED / DISABLED).
	"aws_opensearchserverless_collection",
	// Bundle 5 (#482) — RDS Aurora / multi-AZ cluster (the cluster-level
	// shape sibling to the existing cloud-control-routed db_instance).
	"aws_rds_cluster",
	"aws_resourceexplorer2_index",
	"aws_resourceexplorer2_view",
	"aws_route53_zone",
	// Bundle 6 (#482) — VPC route table. The route set + propagating
	// VGWs are the drift-relevant axes.
	"aws_route_table",
	"aws_s3_bucket",
	// S3 bucket sub-resources (#482 push to 95% coverage). Each maps
	// to an SDK-only sub-resource discoverer already registered in
	// sdkOnlySubresourceTypeConfigs; the per-bucket GetBucket* SDK
	// calls produce the typed payload the new enrichers fan into
	// ImportedResource.Attrs.
	"aws_s3_bucket_lifecycle_configuration",
	// Bundle 11 (#482) — S3 bucket policy. Identity is (bucket); the
	// `policy` JSON is the security-critical surface (who can
	// Get/Put/Delete objects). Drift is high-signal.
	"aws_s3_bucket_policy",
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
	// Bundle 11 (#482) — SNS topic subscription. Identity is (arn);
	// (topic_arn, protocol, endpoint) is the fanout-wiring axis.
	// filter_policy controls delivery filtering; drift retargets the
	// fanout silently.
	"aws_sns_topic_subscription",
	"aws_sqs_queue",
	// Bundle 8 (#482) — SSM Parameter Store entry. Config + secret
	// material referenced by deploys; `value` is sensitive and drift
	// tracking flags out-of-band rotation.
	"aws_ssm_parameter",
	"aws_subnet",
	"aws_vpc",
	// Bundle 9 (#482) — VPC DHCP option set. Domain name + DNS server
	// + NTP server config that VPC instances receive at lease time.
	"aws_vpc_dhcp_options",
	"aws_vpc_endpoint",
	// Bundle 9 (#482) — Modern VPC SG egress + ingress rule resources.
	// Each row is a single rule (vs. the legacy `aws_security_group`
	// embedded `ingress` / `egress` blocks). Identity is the rule id;
	// the (protocol, from_port, to_port, cidr/sg ref) tuple is the
	// security-critical surface.
	"aws_vpc_security_group_egress_rule",
	"aws_vpc_security_group_ingress_rule",
	// Bundle 9 (#482) — WAFv2 web ACL. The top-level firewall (vs. the
	// existing wafv2_web_acl_association which is the binding row);
	// `rule` is the load-bearing rules surface, default_action gates
	// fallthrough behavior.
	"aws_wafv2_web_acl",
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

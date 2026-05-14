# Supported Resources

This document lists all cloud resources supported by InsideOut, with two
orthogonal axes:

- **Insideout-managed** — resource types declared in preset modules under
  `aws/<module>/` or `gcp/<module>/`. These are the types InsideOut creates
  on-apply when composing a stack.
- **Importable** — resource types the discovery framework
  (`cmd/insideout-import/<cloud>discover/` + `pkg/insideout-import/registry/`)
  can detect, ingest, and re-render as Terraform.

A resource is **Both** when it's preset-declared and importable, **Preset-only**
when InsideOut creates it but discovery doesn't yet see it, **Registry-only**
when discovery sees it but no preset module declares it (typically because
the type is produced by a wrapped registry module like
`terraform-aws-modules/vpc/aws`).

Coverage summary (last updated 2026-05-14):

| Cloud | Preset-declared | Importable | Both | Preset-only | Registry-only |
|-------|----------------:|-----------:|-----:|------------:|--------------:|
| AWS | 70 | 109 | 70 | 0 | 39 |
| GCP | 47 | 54 | 47 | 0 | 7 |

## AWS

Every directly-declared AWS resource type is also importable via the
discovery registry — there are no preset-only gaps. The Registry-only
subsection captures the types emitted by wrapped community modules
(`terraform-aws-modules/vpc/aws` and `terraform-aws-modules/eks/aws`),
plus auxiliary types the discovery pipeline can ingest even though no
preset creates them directly.

### `aws/alb`

| TF type | Coverage |
|---------|----------|
| `aws_cloudwatch_metric_alarm` | Both |
| `aws_lb` | Both |
| `aws_lb_listener` | Both |
| `aws_lb_target_group` | Both |
| `aws_s3_bucket` | Both |
| `aws_s3_bucket_lifecycle_configuration` | Both |
| `aws_s3_bucket_policy` | Both |
| `aws_s3_bucket_public_access_block` | Both |
| `aws_security_group` | Both |

### `aws/apigateway`

| TF type | Coverage |
|---------|----------|
| `aws_apigatewayv2_api` | Both |
| `aws_apigatewayv2_api_mapping` | Both |
| `aws_apigatewayv2_domain_name` | Both |
| `aws_apigatewayv2_stage` | Both |
| `aws_cloudwatch_metric_alarm` | Both |

### `aws/backups`

| TF type | Coverage |
|---------|----------|
| `aws_backup_plan` | Both |
| `aws_backup_selection` | Both |
| `aws_backup_vault` | Both |
| `aws_iam_role` | Both |
| `aws_iam_role_policy_attachment` | Both |

### `aws/bastion`

| TF type | Coverage |
|---------|----------|
| `aws_cloudwatch_metric_alarm` | Both |
| `aws_iam_instance_profile` | Both |
| `aws_iam_role` | Both |
| `aws_iam_role_policy_attachment` | Both |
| `aws_instance` | Both |
| `aws_security_group` | Both |

### `aws/bedrock`

| TF type | Coverage |
|---------|----------|
| `aws_bedrock_guardrail` | Both |
| `aws_bedrock_model_invocation_logging_configuration` | Both |
| `aws_cloudwatch_log_group` | Both |
| `aws_iam_role` | Both |
| `aws_iam_role_policy` | Both |
| `aws_opensearchserverless_access_policy` | Both |

### `aws/cloudfront`

| TF type | Coverage |
|---------|----------|
| `aws_cloudfront_distribution` | Both |
| `aws_cloudfront_monitoring_subscription` | Both |
| `aws_cloudfront_origin_access_identity` | Both |
| `aws_s3_bucket` | Both |
| `aws_s3_bucket_policy` | Both |
| `aws_s3_bucket_public_access_block` | Both |

### `aws/cloudwatchlogs`

| TF type | Coverage |
|---------|----------|
| `aws_cloudwatch_log_group` | Both |
| `aws_cloudwatch_log_stream` | Both |
| `aws_iam_policy` | Both |
| `aws_iam_role` | Both |
| `aws_iam_role_policy_attachment` | Both |

### `aws/cloudwatchmonitoring`

| TF type | Coverage |
|---------|----------|
| `aws_cloudwatch_dashboard` | Both |
| `aws_cloudwatch_metric_alarm` | Both |
| `aws_sns_topic` | Both |
| `aws_sns_topic_subscription` | Both |

### `aws/codepipeline`

Placeholder — CodePipeline CI/CD; no resources declared yet.

### `aws/cognito`

| TF type | Coverage |
|---------|----------|
| `aws_cognito_identity_provider` | Both |
| `aws_cognito_user_pool` | Both |
| `aws_cognito_user_pool_client` | Both |
| `aws_cognito_user_pool_domain` | Both |

### `aws/composer`

Placeholder — stack composer/scaffolding helper; no resources declared.

### `aws/datadog`

Placeholder — Datadog observability integration; no resources declared yet.

### `aws/dynamodb`

| TF type | Coverage |
|---------|----------|
| `aws_cloudwatch_metric_alarm` | Both |
| `aws_dynamodb_contributor_insights` | Both |
| `aws_dynamodb_table` | Both |

### `aws/ec2`

| TF type | Coverage |
|---------|----------|
| `aws_cloudwatch_metric_alarm` | Both |
| `aws_iam_instance_profile` | Both |
| `aws_iam_role` | Both |
| `aws_iam_role_policy_attachment` | Both |
| `aws_instance` | Both |
| `aws_key_pair` | Both |
| `aws_security_group` | Both |
| `aws_vpc_security_group_ingress_rule` | Both |

### `aws/ecs`

| TF type | Coverage |
|---------|----------|
| `aws_cloudwatch_metric_alarm` | Both |
| `aws_ecs_cluster` | Both |
| `aws_ecs_cluster_capacity_providers` | Both |
| `aws_service_discovery_private_dns_namespace` | Both |

### `aws/eks_nodegroup`

| TF type | Coverage |
|---------|----------|
| `aws_autoscaling_group_tag` | Both |
| `aws_cloudwatch_metric_alarm` | Both |
| `aws_eks_addon` | Both |
| `aws_eks_node_group` | Both |
| `aws_iam_role` | Both |
| `aws_iam_role_policy_attachment` | Both |
| `aws_iam_service_linked_role` | Both |

### `aws/elasticache`

| TF type | Coverage |
|---------|----------|
| `aws_cloudwatch_log_group` | Both |
| `aws_cloudwatch_metric_alarm` | Both |
| `aws_elasticache_parameter_group` | Both |
| `aws_elasticache_replication_group` | Both |
| `aws_elasticache_subnet_group` | Both |
| `aws_security_group` | Both |

### `aws/githubactions`

Placeholder — GitHub Actions OIDC; no resources declared yet.

### `aws/grafana`

Placeholder — Amazon Managed Grafana; no resources declared yet.

### `aws/inspector`

Placeholder — AWS Inspector; no `*.tf` files yet.

### `aws/kms`

| TF type | Coverage |
|---------|----------|
| `aws_kms_alias` | Both |
| `aws_kms_key` | Both |

### `aws/lambda`

| TF type | Coverage |
|---------|----------|
| `aws_cloudwatch_log_group` | Both |
| `aws_cloudwatch_metric_alarm` | Both |
| `aws_iam_role` | Both |
| `aws_iam_role_policy_attachment` | Both |
| `aws_lambda_function` | Both |
| `aws_security_group` | Both |

### `aws/msk`

| TF type | Coverage |
|---------|----------|
| `aws_cloudwatch_log_group` | Both |
| `aws_cloudwatch_metric_alarm` | Both |
| `aws_iam_service_linked_role` | Both |
| `aws_msk_cluster` | Both |
| `aws_msk_configuration` | Both |
| `aws_security_group` | Both |

### `aws/opensearch`

| TF type | Coverage |
|---------|----------|
| `aws_cloudwatch_log_group` | Both |
| `aws_cloudwatch_log_resource_policy` | Both |
| `aws_cloudwatch_metric_alarm` | Both |
| `aws_iam_service_linked_role` | Both |
| `aws_opensearch_domain` | Both |
| `aws_opensearchserverless_collection` | Both |
| `aws_opensearchserverless_security_policy` | Both |
| `aws_security_group` | Both |

### `aws/rds`

| TF type | Coverage |
|---------|----------|
| `aws_cloudwatch_metric_alarm` | Both |
| `aws_db_instance` | Both |
| `aws_db_subnet_group` | Both |
| `aws_iam_role` | Both |
| `aws_iam_role_policy_attachment` | Both |
| `aws_security_group` | Both |

### `aws/resource`

Wraps registry module: `terraform-aws-modules/eks/aws` ~> 21.0 (EKS cluster control plane)

| TF type | Coverage |
|---------|----------|
| `aws_iam_role` | Both |
| `aws_iam_role_policy_attachment` | Both |

### `aws/s3`

| TF type | Coverage |
|---------|----------|
| `aws_s3_bucket` | Both |
| `aws_s3_bucket_lifecycle_configuration` | Both |
| `aws_s3_bucket_ownership_controls` | Both |
| `aws_s3_bucket_public_access_block` | Both |
| `aws_s3_bucket_server_side_encryption_configuration` | Both |
| `aws_s3_bucket_versioning` | Both |

### `aws/secretsmanager`

| TF type | Coverage |
|---------|----------|
| `aws_secretsmanager_secret` | Both |

### `aws/splunk`

Placeholder — Splunk log forwarding; no resources declared yet.

### `aws/sqs`

| TF type | Coverage |
|---------|----------|
| `aws_cloudwatch_metric_alarm` | Both |
| `aws_sqs_queue` | Both |

### `aws/vpc`

Wraps registry module: `terraform-aws-modules/vpc/aws` ~> 6.0

| TF type | Coverage |
|---------|----------|
| `aws_vpc_endpoint` | Both |

### `aws/waf`

| TF type | Coverage |
|---------|----------|
| `aws_wafv2_web_acl` | Both |
| `aws_wafv2_web_acl_association` | Both |

### Registry-only AWS types

Importable types that no preset module declares directly. Most are
emitted by wrapped community modules (VPC primitives from
`terraform-aws-modules/vpc/aws`, EKS control-plane resources from
`terraform-aws-modules/eks/aws`), or are auxiliary discovery surfaces
(account-scoped singletons, classic API Gateway, route53, etc.).

| TF type | Origin (typical) |
|---------|------------------|
| `aws_acm_certificate` | ACM (discovery-only) |
| `aws_api_gateway_deployment` | REST API Gateway v1 (discovery-only) |
| `aws_api_gateway_resource` | REST API Gateway v1 (discovery-only) |
| `aws_api_gateway_stage` | REST API Gateway v1 (discovery-only) |
| `aws_apigatewayv2_authorizer` | HTTP/WebSocket API authorizer (discovery-only) |
| `aws_apigatewayv2_integration` | HTTP/WebSocket API integration (discovery-only) |
| `aws_apigatewayv2_route` | HTTP/WebSocket API route (discovery-only) |
| `aws_autoscaling_group` | EKS node group ASG (via terraform-aws-modules/eks) |
| `aws_cloudfront_function` | CloudFront edge function (discovery-only) |
| `aws_cloudwatch_event_rule` | EventBridge rule (discovery-only) |
| `aws_cognito_resource_server` | Cognito OAuth resource server (discovery-only) |
| `aws_db_parameter_group` | RDS parameter group (discovery-only) |
| `aws_ebs_volume` | EC2/EKS-attached EBS volumes (discovery-only) |
| `aws_eip` | Elastic IP — emitted by VPC NAT gateway |
| `aws_eks_access_entry` | EKS access entry (via terraform-aws-modules/eks) |
| `aws_eks_cluster` | EKS control plane (via terraform-aws-modules/eks) |
| `aws_eks_fargate_profile` | EKS Fargate profile (via terraform-aws-modules/eks) |
| `aws_eks_pod_identity_association` | EKS pod identity (via terraform-aws-modules/eks) |
| `aws_iam_group` | IAM group (discovery-only) |
| `aws_iam_user` | IAM user (discovery-only) |
| `aws_internet_gateway` | VPC IGW (via terraform-aws-modules/vpc) |
| `aws_lambda_alias` | Lambda alias (discovery-only) |
| `aws_lambda_event_source_mapping` | Lambda event source mapping (discovery-only) |
| `aws_lambda_function_url` | Lambda function URL (discovery-only) |
| `aws_lambda_permission` | Lambda permission (discovery-only) |
| `aws_launch_template` | EKS managed node group launch template |
| `aws_nat_gateway` | VPC NAT gateway (via terraform-aws-modules/vpc) |
| `aws_network_acl` | VPC NACL (via terraform-aws-modules/vpc) |
| `aws_network_interface` | ENI (discovery-only) |
| `aws_resourceexplorer2_index` | Resource Explorer index (discovery-only) |
| `aws_resourceexplorer2_view` | Resource Explorer view (discovery-only) |
| `aws_route53_zone` | Route53 hosted zone (discovery-only) |
| `aws_route_table` | VPC route table (via terraform-aws-modules/vpc) |
| `aws_secretsmanager_secret_rotation` | Secrets Manager rotation (discovery-only) |
| `aws_ssm_parameter` | SSM parameter (discovery-only) |
| `aws_subnet` | VPC subnet (via terraform-aws-modules/vpc) |
| `aws_vpc` | VPC (via terraform-aws-modules/vpc) |
| `aws_vpc_dhcp_options` | VPC DHCP options (via terraform-aws-modules/vpc) |
| `aws_vpc_security_group_egress_rule` | SG egress rule (via various wrappers, discovery) |

### Preset-only AWS types

None — every preset-declared AWS resource type is also registered in
the discovery registry.

## GCP

After Bundle G4 the GCP catalog reached full parity — every preset-
declared GCP resource type is also importable via the discovery
registry. The Registry-only subsection captures types emitted by the
wrapped community modules (`terraform-google-modules/network/google`,
`terraform-google-modules/kubernetes-engine/google`, and
`GoogleCloudPlatform/sql-db/google`).

### `gcp/api_gateway`

| TF type | Coverage |
|---------|----------|
| `google_api_gateway_api` | Both |
| `google_api_gateway_api_config` | Both |
| `google_api_gateway_gateway` | Both |
| `google_monitoring_alert_policy` | Both |
| `google_project_service` | Both |

### `gcp/backups`

| TF type | Coverage |
|---------|----------|
| `google_compute_resource_policy` | Both |
| `google_storage_bucket` | Both |

### `gcp/bastion`

| TF type | Coverage |
|---------|----------|
| `google_compute_firewall` | Both |
| `google_compute_instance` | Both |
| `google_monitoring_alert_policy` | Both |
| `google_project_iam_member` | Both |
| `google_service_account` | Both |

### `gcp/cloud_armor`

| TF type | Coverage |
|---------|----------|
| `google_compute_security_policy` | Both |

### `gcp/cloud_build`

| TF type | Coverage |
|---------|----------|
| `google_cloudbuild_trigger` | Both |
| `google_project_iam_member` | Both |
| `google_project_service` | Both |
| `google_secret_manager_secret` | Both |
| `google_secret_manager_secret_iam_member` | Both |
| `google_secret_manager_secret_version` | Both |
| `google_service_account` | Both |

### `gcp/cloud_functions`

| TF type | Coverage |
|---------|----------|
| `google_cloudfunctions2_function` | Both |
| `google_cloudfunctions2_function_iam_member` | Both |
| `google_monitoring_alert_policy` | Both |
| `google_storage_bucket` | Both |
| `google_storage_bucket_object` | Both |

### `gcp/cloud_logging`

| TF type | Coverage |
|---------|----------|
| `google_logging_project_sink` | Both |
| `google_storage_bucket` | Both |
| `google_storage_bucket_iam_member` | Both |

### `gcp/cloud_monitoring`

| TF type | Coverage |
|---------|----------|
| `google_monitoring_dashboard` | Both |
| `google_monitoring_notification_channel` | Both |

### `gcp/cloud_run`

| TF type | Coverage |
|---------|----------|
| `google_cloud_run_v2_service` | Both |
| `google_cloud_run_v2_service_iam_member` | Both |
| `google_monitoring_alert_policy` | Both |

### `gcp/cloudsql`

Wraps registry module: `GoogleCloudPlatform/sql-db/google//modules/{postgresql,mysql}` ~> 21.0

| TF type | Coverage |
|---------|----------|
| `google_compute_global_address` | Both |
| `google_monitoring_alert_policy` | Both |
| `google_service_networking_connection` | Both |

### `gcp/compute`

| TF type | Coverage |
|---------|----------|
| `google_compute_instance` | Both |
| `google_monitoring_alert_policy` | Both |

### `gcp/firestore`

| TF type | Coverage |
|---------|----------|
| `google_firestore_database` | Both |
| `google_monitoring_alert_policy` | Both |

### `gcp/gcs`

| TF type | Coverage |
|---------|----------|
| `google_storage_bucket` | Both |

### `gcp/gke`

Wraps registry module: `terraform-google-modules/kubernetes-engine/google//modules/private-cluster` ~> 33.0

| TF type | Coverage |
|---------|----------|
| `google_monitoring_alert_policy` | Both |

### `gcp/identity_platform`

| TF type | Coverage |
|---------|----------|
| `google_identity_platform_config` | Both |
| `google_identity_platform_default_supported_idp_config` | Both |
| `google_project_service` | Both |

### `gcp/kms`

| TF type | Coverage |
|---------|----------|
| `google_kms_crypto_key` | Both |
| `google_kms_crypto_key_iam_binding` | Both |
| `google_kms_key_ring` | Both |

### `gcp/loadbalancer`

| TF type | Coverage |
|---------|----------|
| `google_compute_backend_service` | Both |
| `google_compute_global_address` | Both |
| `google_compute_global_forwarding_rule` | Both |
| `google_compute_health_check` | Both |
| `google_compute_managed_ssl_certificate` | Both |
| `google_compute_target_http_proxy` | Both |
| `google_compute_target_https_proxy` | Both |
| `google_compute_url_map` | Both |
| `google_monitoring_alert_policy` | Both |

### `gcp/memorystore`

| TF type | Coverage |
|---------|----------|
| `google_monitoring_alert_policy` | Both |
| `google_redis_instance` | Both |

### `gcp/pubsub`

| TF type | Coverage |
|---------|----------|
| `google_monitoring_alert_policy` | Both |
| `google_pubsub_subscription` | Both |
| `google_pubsub_topic` | Both |

### `gcp/secretmanager`

| TF type | Coverage |
|---------|----------|
| `google_secret_manager_secret` | Both |
| `google_secret_manager_secret_iam_binding` | Both |
| `google_secret_manager_secret_version` | Both |

### `gcp/vertex_ai`

| TF type | Coverage |
|---------|----------|
| `google_vertex_ai_dataset` | Both |

### `gcp/vpc`

Wraps registry module: `terraform-google-modules/network/google` ~> 9.0 + `terraform-google-modules/cloud-nat/google` ~> 5.0

| TF type | Coverage |
|---------|----------|
| `google_compute_firewall` | Both |
| `google_compute_router` | Both |
| `google_vpc_access_connector` | Both |

### Registry-only GCP types

Importable types that no preset module declares directly. All seven
are emitted by the wrapped community modules listed above.

| TF type | Origin (typical) |
|---------|------------------|
| `google_compute_address` | VPC regional address (via terraform-google-modules/cloud-nat) |
| `google_compute_forwarding_rule` | regional forwarding rule (discovery-only) |
| `google_compute_network` | VPC network (via terraform-google-modules/network) |
| `google_container_cluster` | GKE cluster (via terraform-google-modules/kubernetes-engine) |
| `google_container_node_pool` | GKE node pool (via terraform-google-modules/kubernetes-engine) |
| `google_sql_database_instance` | Cloud SQL instance (via GoogleCloudPlatform/sql-db) |
| `google_sql_user` | Cloud SQL user (via GoogleCloudPlatform/sql-db) |

### Preset-only GCP types

None — Bundle G4 closed the last gaps. Every preset-declared GCP
resource type is registered in the discovery registry.

## How to regenerate

This doc is updated by hand after each bundle that adds discoverers or
presets. The accurate source-of-truth is:

- Importable: `pkg/insideout-import/registry/registry.go::{awsTypes,gcpTypes}`
- Preset-declared: grep `^resource "` across `aws/**/*.tf` and `gcp/**/*.tf`
  (excluding `_shared/` and `_*/` dirs, and excluding `random_*` / `time_*`
  provider helper resources that are not part of AWS/GCP coverage).

To check parity in CI, run:

```bash
go test ./pkg/insideout-import/registry/...
```

The registry's go test pins the lists; preset-declared types are validated by
the per-module `terraform validate` jobs.

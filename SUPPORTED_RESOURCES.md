# Supported Resources

Per-type Capabilities matrix for every cloud resource the InsideOut
discovery + composition pipeline supports. Five orthogonal axes:

- **Discoverable** — the discovery registry can list resources of
  this type from a live cloud account.
- **Enrichable** — at least one `AttributeEnricher` is registered
  in the per-cloud discoverer (fetches extended attributes beyond
  the bare list call).
- **DriftDetectable** — the curated `policy.Map` for this type has
  at least one field with a non-empty `DriftSemantic` axis.
- **MetricsAvailable** — the metrics bindings registry exposes a
  default metric surface for this type.
- **AgentEditable** — at least one field in the curated `policy.Map`
  carries `EditChatSafe` or `EditRequiresApproval` — i.e. an agent
  may write to it through the policy-gated edit path.

This document is generated from `cmd/imported-codegen capabilities`
and is checked in lockstep with the runtime registries. See the
`How to regenerate` section at the bottom for the regen command.

## Summary

- **AWS:** 129 types · 84% Discoverable · 84% Enrichable · 100% DriftDetectable · 67% MetricsAvailable · 91% AgentEditable
- **GCP:** 59 types · 92% Discoverable · 92% Enrichable · 100% DriftDetectable · 81% MetricsAvailable · 90% AgentEditable

## AWS

| TF Type | Discoverable | Enrichable | DriftDetectable | MetricsAvailable | AgentEditable |
|---|---|---|---|---|---|
| `aws_acm_certificate` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_acm_certificate_validation` | – | – | ✓ | – | ✓ |
| `aws_api_gateway_deployment` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_api_gateway_resource` | ✓ | ✓ | ✓ | – | – |
| `aws_api_gateway_stage` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_apigatewayv2_api` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_apigatewayv2_api_mapping` | ✓ | ✓ | ✓ | – | ✓ |
| `aws_apigatewayv2_authorizer` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_apigatewayv2_domain_name` | ✓ | ✓ | ✓ | ✓ | – |
| `aws_apigatewayv2_integration` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_apigatewayv2_route` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_apigatewayv2_stage` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_appautoscaling_policy` | – | – | ✓ | – | ✓ |
| `aws_appautoscaling_target` | – | – | ✓ | – | ✓ |
| `aws_athena_workgroup` | – | – | ✓ | – | ✓ |
| `aws_autoscaling_group` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_autoscaling_group_tag` | ✓ | ✓ | ✓ | – | ✓ |
| `aws_backup_plan` | ✓ | ✓ | ✓ | ✓ | – |
| `aws_backup_selection` | ✓ | ✓ | ✓ | – | ✓ |
| `aws_backup_vault` | ✓ | ✓ | ✓ | ✓ | – |
| `aws_bedrock_guardrail` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_bedrock_model_invocation_logging_configuration` | ✓ | ✓ | ✓ | – | ✓ |
| `aws_cloudfront_distribution` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_cloudfront_function` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_cloudfront_monitoring_subscription` | ✓ | ✓ | ✓ | – | ✓ |
| `aws_cloudfront_origin_access_identity` | ✓ | ✓ | ✓ | – | ✓ |
| `aws_cloudtrail` | – | – | ✓ | – | ✓ |
| `aws_cloudwatch_dashboard` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_cloudwatch_event_bus` | – | – | ✓ | – | – |
| `aws_cloudwatch_event_rule` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_cloudwatch_log_group` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_cloudwatch_log_resource_policy` | ✓ | ✓ | ✓ | – | ✓ |
| `aws_cloudwatch_log_stream` | ✓ | ✓ | ✓ | ✓ | – |
| `aws_cloudwatch_metric_alarm` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_codebuild_project` | – | – | ✓ | – | ✓ |
| `aws_codedeploy_app` | – | – | ✓ | – | ✓ |
| `aws_codepipeline` | – | – | ✓ | – | ✓ |
| `aws_cognito_identity_provider` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_cognito_resource_server` | ✓ | ✓ | ✓ | – | ✓ |
| `aws_cognito_user_pool` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_cognito_user_pool_client` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_cognito_user_pool_domain` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_db_instance` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_db_parameter_group` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_db_subnet_group` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_dynamodb_contributor_insights` | ✓ | ✓ | ✓ | ✓ | – |
| `aws_dynamodb_global_table` | – | – | ✓ | – | ✓ |
| `aws_dynamodb_table` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_ebs_volume` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_ecs_cluster` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_ecs_cluster_capacity_providers` | ✓ | ✓ | ✓ | – | ✓ |
| `aws_ecs_service` | – | – | ✓ | ✓ | ✓ |
| `aws_ecs_task_definition` | – | – | ✓ | – | ✓ |
| `aws_efs_file_system` | – | – | ✓ | ✓ | ✓ |
| `aws_eip` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_eks_access_entry` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_eks_addon` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_eks_cluster` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_eks_fargate_profile` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_eks_node_group` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_eks_pod_identity_association` | ✓ | ✓ | ✓ | – | ✓ |
| `aws_elasticache_parameter_group` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_elasticache_replication_group` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_elasticache_subnet_group` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_glue_catalog_database` | – | – | ✓ | – | ✓ |
| `aws_glue_job` | – | – | ✓ | – | ✓ |
| `aws_iam_group` | ✓ | ✓ | ✓ | ✓ | – |
| `aws_iam_instance_profile` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_iam_policy` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_iam_role` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_iam_role_policy` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_iam_role_policy_attachment` | ✓ | ✓ | ✓ | ✓ | – |
| `aws_iam_service_linked_role` | ✓ | ✓ | ✓ | – | ✓ |
| `aws_iam_user` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_instance` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_internet_gateway` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_key_pair` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_kinesis_stream` | – | – | ✓ | ✓ | ✓ |
| `aws_kms_alias` | ✓ | ✓ | ✓ | ✓ | – |
| `aws_kms_key` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_lambda_alias` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_lambda_event_source_mapping` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_lambda_function` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_lambda_function_url` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_lambda_layer_version` | – | – | ✓ | – | ✓ |
| `aws_lambda_permission` | ✓ | ✓ | ✓ | – | ✓ |
| `aws_launch_template` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_lb` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_lb_listener` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_lb_target_group` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_msk_cluster` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_msk_configuration` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_nat_gateway` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_network_acl` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_network_interface` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_opensearch_domain` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_opensearchserverless_access_policy` | ✓ | ✓ | ✓ | – | ✓ |
| `aws_opensearchserverless_collection` | ✓ | ✓ | ✓ | – | ✓ |
| `aws_opensearchserverless_security_policy` | ✓ | ✓ | ✓ | – | ✓ |
| `aws_rds_cluster` | – | – | ✓ | ✓ | ✓ |
| `aws_resourceexplorer2_index` | ✓ | ✓ | ✓ | – | ✓ |
| `aws_resourceexplorer2_view` | ✓ | ✓ | ✓ | – | ✓ |
| `aws_route53_record` | – | – | ✓ | – | ✓ |
| `aws_route53_zone` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_route_table` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_s3_bucket` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_s3_bucket_lifecycle_configuration` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_s3_bucket_ownership_controls` | ✓ | ✓ | ✓ | – | ✓ |
| `aws_s3_bucket_policy` | ✓ | ✓ | ✓ | – | ✓ |
| `aws_s3_bucket_public_access_block` | ✓ | ✓ | ✓ | – | ✓ |
| `aws_s3_bucket_server_side_encryption_configuration` | ✓ | ✓ | ✓ | – | ✓ |
| `aws_s3_bucket_versioning` | ✓ | ✓ | ✓ | – | ✓ |
| `aws_secretsmanager_secret` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_secretsmanager_secret_rotation` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_security_group` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_service_discovery_private_dns_namespace` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_sfn_state_machine` | – | – | ✓ | – | ✓ |
| `aws_sns_topic` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_sns_topic_subscription` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_sqs_queue` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_ssm_parameter` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_subnet` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_vpc` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_vpc_dhcp_options` | ✓ | ✓ | ✓ | – | ✓ |
| `aws_vpc_endpoint` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_vpc_security_group_egress_rule` | ✓ | ✓ | ✓ | – | ✓ |
| `aws_vpc_security_group_ingress_rule` | ✓ | ✓ | ✓ | – | ✓ |
| `aws_wafv2_web_acl` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `aws_wafv2_web_acl_association` | ✓ | ✓ | ✓ | ✓ | – |

## GCP

| TF Type | Discoverable | Enrichable | DriftDetectable | MetricsAvailable | AgentEditable |
|---|---|---|---|---|---|
| `google_api_gateway_api` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `google_api_gateway_api_config` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `google_api_gateway_gateway` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `google_certificate_manager_certificate` | – | – | ✓ | – | ✓ |
| `google_certificate_manager_certificate_map` | – | – | ✓ | – | ✓ |
| `google_certificate_manager_certificate_map_entry` | – | – | ✓ | – | ✓ |
| `google_cloud_run_v2_service` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `google_cloud_run_v2_service_iam_member` | ✓ | ✓ | ✓ | – | – |
| `google_cloudbuild_trigger` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `google_cloudfunctions2_function` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `google_cloudfunctions2_function_iam_member` | ✓ | ✓ | ✓ | – | – |
| `google_compute_address` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `google_compute_backend_service` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `google_compute_firewall` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `google_compute_forwarding_rule` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `google_compute_global_address` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `google_compute_global_forwarding_rule` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `google_compute_health_check` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `google_compute_instance` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `google_compute_managed_ssl_certificate` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `google_compute_network` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `google_compute_resource_policy` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `google_compute_router` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `google_compute_security_policy` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `google_compute_target_http_proxy` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `google_compute_target_https_proxy` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `google_compute_url_map` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `google_container_cluster` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `google_container_node_pool` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `google_dns_managed_zone` | – | – | ✓ | – | ✓ |
| `google_dns_record_set` | – | – | ✓ | – | ✓ |
| `google_firestore_database` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `google_identity_platform_config` | ✓ | ✓ | ✓ | – | ✓ |
| `google_identity_platform_default_supported_idp_config` | ✓ | ✓ | ✓ | – | ✓ |
| `google_kms_crypto_key` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `google_kms_crypto_key_iam_binding` | ✓ | ✓ | ✓ | – | ✓ |
| `google_kms_key_ring` | ✓ | ✓ | ✓ | ✓ | – |
| `google_logging_project_sink` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `google_monitoring_alert_policy` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `google_monitoring_dashboard` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `google_monitoring_notification_channel` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `google_project_iam_member` | ✓ | ✓ | ✓ | ✓ | – |
| `google_project_service` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `google_pubsub_subscription` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `google_pubsub_topic` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `google_redis_instance` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `google_secret_manager_secret` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `google_secret_manager_secret_iam_binding` | ✓ | ✓ | ✓ | – | ✓ |
| `google_secret_manager_secret_iam_member` | ✓ | ✓ | ✓ | ✓ | – |
| `google_secret_manager_secret_version` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `google_service_account` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `google_service_networking_connection` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `google_sql_database_instance` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `google_sql_user` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `google_storage_bucket` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `google_storage_bucket_iam_member` | ✓ | ✓ | ✓ | ✓ | – |
| `google_storage_bucket_object` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `google_vertex_ai_dataset` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `google_vpc_access_connector` | ✓ | ✓ | ✓ | ✓ | ✓ |

## How to regenerate

```bash
make regen-supported-resources
# or, directly:
go run ./cmd/imported-codegen supported-resources --output SUPPORTED_RESOURCES.md
```

CI runs `make verify-supported-resources`, which re-renders the
document and fails the build if the committed copy is out of
date.

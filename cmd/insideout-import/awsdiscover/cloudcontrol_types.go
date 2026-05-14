package awsdiscover

import "strings"

// cloudControlTypeConfigs is the registry of Terraform resource types
// routed through the generic Cloud Control discoverer. Each entry maps
// one TFType to a Cloud-Formation TypeName plus per-type extractors for
// import-ID, name-hint, native-ID, and tag-shape. The list is iterated
// at aggregator construction time (NewAWSDiscovererWithConcurrency in
// awsdiscover.go) to populate byType in one shot.
//
// Tag-extraction conventions:
//   - "Tags" as []{Key, Value} (most modern services): extractTagList
//   - "Tags" as map[string]string (older services like Backup):
//     extractStringMap
//   - Some services use a service-specific field name
//     (BackupVaultTags, BackupPlanTags) — name follows the
//     CloudFormation schema.
//
// Discoverable types that stay hand-rolled and are NOT in this table:
//   - aws_apigatewayv2_stage — Cloud Control returns
//     UnsupportedActionException for READ on AWS::ApiGatewayV2::Stage
//     (verified in live smoke, issue #406). The per-service SDK
//     discoverer in apigatewayv2_stage.go handles this type.
//   - aws_bedrock_guardrail — per-version listing semantics; CFN type
//     only models the latest version. The per-service SDK discoverer
//     in bedrock_guardrail.go handles this type.
//   - aws_resourceexplorer2_index / aws_resourceexplorer2_view —
//     cross-region ARN dedup quirk (#336). The per-service SDK
//     discoverers in resourceexplorer2_*.go handle these types.
//
// Adding a new type means: (1) confirm Cloud Control supports both
// ListResources and GetResource for the AWS::Service::Resource
// TypeName, (2) confirm the Cloud Control primary identifier matches
// the Terraform import format (or write an ImportIDFromIdentifier
// rewriter), (3) confirm the GetResource properties payload carries
// tags in a recognizable shape, (4) add an arnRule in arn_rules.go so
// the RGT prefetcher can map ARNs → (cfnType, identifier), (5) append
// the config below, (6) extend
// pkg/insideout-import/registry/registry.go::awsTypes, (7) extend
// pkg/composer/imported/category.go::categoryByTFType, (8) extend
// pkg/insideout-import/permissions/aws.json with cloudcontrol:* +
// per-CFN-type Read permissions.
var cloudControlTypeConfigs = []cloudControlConfig{
	// =====================================================================
	// Backup
	// =====================================================================
	{
		TFType:                  "aws_backup_vault",
		CloudFormationType:      "AWS::Backup::BackupVault",
		Slug:                    "backup_vault",
		ImportIDFromIdentifier:  passthroughImportID,
		NameHintFromProperties:  nameOrIdentifier("BackupVaultName"),
		NativeIDsFromProperties: arnUnderKey("BackupVaultArn"),
		TagsFromProperties: func(props map[string]any) map[string]string {
			return extractStringMap(props, "BackupVaultTags")
		},
	},
	{
		TFType:                 "aws_backup_plan",
		CloudFormationType:     "AWS::Backup::BackupPlan",
		Slug:                   "backup_plan",
		ImportIDFromIdentifier: passthroughImportID,
		// Name is nested under BackupPlan.BackupPlanName.
		NameHintFromProperties: func(identifier string, props map[string]any) string {
			if planObj, ok := props["BackupPlan"].(map[string]any); ok {
				if name := extractString(planObj, "BackupPlanName"); name != "" {
					return name
				}
			}
			return identifier
		},
		NativeIDsFromProperties: arnUnderKey("BackupPlanArn"),
		TagsFromProperties: func(props map[string]any) map[string]string {
			return extractStringMap(props, "BackupPlanTags")
		},
	},
	{
		// AWS::Backup::BackupSelection — untaggable (CFN schema has no
		// Tags property; selections inherit policies from their parent
		// BackupPlan). RGT does not surface selection ARNs, so the
		// cache-miss ListResources fallback path always runs for this
		// type. SkipProjectTagFilter bypasses the legacy Project filter
		// — without it the empty tag bag would cause every selection
		// to be silently dropped on --project scans.
		//
		// Cloud Control's primary identifier is `<SelectionId>_<BackupPlanId>`
		// (underscore-separated, verified live). Terraform's import
		// format is `<SelectionId>|<BackupPlanId>` (pipe-separated).
		TFType:               "aws_backup_selection",
		CloudFormationType:   "AWS::Backup::BackupSelection",
		Slug:                 "backup_selection",
		SkipProjectTagFilter: true,
		ImportIDFromIdentifier: func(identifier string, _ map[string]any) string {
			return strings.Replace(identifier, "_", "|", 1)
		},
		NameHintFromProperties: func(identifier string, props map[string]any) string {
			if sel, ok := props["BackupSelection"].(map[string]any); ok {
				if name := extractString(sel, "SelectionName"); name != "" {
					return name
				}
			}
			// Fall back to the SelectionId tail of the compound id.
			if idx := strings.Index(identifier, "_"); idx != -1 {
				return identifier[:idx]
			}
			return identifier
		},
		NativeIDsFromProperties: func(identifier string, _ map[string]any) map[string]string {
			// Split `<SelectionId>_<BackupPlanId>` into structured IDs.
			// Always stamp both keys so downstream readers indexing
			// by `backup_plan_id` get an explicit empty string rather
			// than a silent missing-key when the CC identifier is
			// malformed (defensive — Cloud Control's primary
			// identifier always contains the `_`).
			out := map[string]string{"selection_id": "", "backup_plan_id": ""}
			if idx := strings.Index(identifier, "_"); idx != -1 {
				out["selection_id"] = identifier[:idx]
				out["backup_plan_id"] = identifier[idx+1:]
			} else {
				out["selection_id"] = identifier
			}
			return out
		},
		TagsFromProperties: emptyTagsExtractor,
	},

	// =====================================================================
	// Messaging — SNS / SQS
	// =====================================================================
	{
		TFType:                 "aws_sns_topic",
		CloudFormationType:     "AWS::SNS::Topic",
		Slug:                   "sns_topic",
		ImportIDFromIdentifier: passthroughImportID,
		NameHintFromProperties: func(identifier string, props map[string]any) string {
			if name := extractString(props, "TopicName"); name != "" {
				return name
			}
			if idx := strings.LastIndex(identifier, ":"); idx != -1 && idx+1 < len(identifier) {
				return identifier[idx+1:]
			}
			return identifier
		},
		NativeIDsFromProperties: func(identifier string, _ map[string]any) map[string]string {
			return map[string]string{"arn": identifier}
		},
		TagsFromProperties: tagsFromKey("Tags"),
	},
	{
		TFType:             "aws_sqs_queue",
		CloudFormationType: "AWS::SQS::Queue",
		Slug:               "sqs",
		// Cloud Control identifier IS the queue URL (constructed by
		// the arnRule from the ARN); Terraform import format for
		// aws_sqs_queue also takes the URL — passthrough.
		ImportIDFromIdentifier: passthroughImportID,
		NameHintFromProperties: nameOrIdentifier("QueueName"),
		NativeIDsFromProperties: func(_ string, props map[string]any) map[string]string {
			arn := extractString(props, "Arn")
			if arn == "" {
				return nil
			}
			return map[string]string{"arn": arn}
		},
		TagsFromProperties: tagsFromKey("Tags"),
	},

	// =====================================================================
	// Observability
	// =====================================================================
	{
		TFType:                  "aws_cloudwatch_metric_alarm",
		CloudFormationType:      "AWS::CloudWatch::Alarm",
		Slug:                    "cloudwatch_metric_alarm",
		ImportIDFromIdentifier:  passthroughImportID,
		NameHintFromProperties:  nameOrIdentifier("AlarmName"),
		NativeIDsFromProperties: arnUnderKey("Arn"),
		TagsFromProperties:      tagsFromKey("Tags"),
	},
	{
		TFType:                 "aws_cloudwatch_dashboard",
		CloudFormationType:     "AWS::CloudWatch::Dashboard",
		Slug:                   "cloudwatch_dashboard",
		ImportIDFromIdentifier: passthroughImportID,
		NameHintFromProperties: nameOrIdentifier("DashboardName"),
		// CloudWatch dashboards do not carry tags in the CFN schema.
		TagsFromProperties: nilTagsExtractor,
	},
	{
		TFType:             "aws_cloudwatch_log_group",
		CloudFormationType: "AWS::Logs::LogGroup",
		Slug:               "cloudwatchlogs",
		// Cloud Control identifier = LogGroupName. Terraform import
		// also takes LogGroupName — passthrough.
		ImportIDFromIdentifier:  passthroughImportID,
		NameHintFromProperties:  nameOrIdentifier("LogGroupName"),
		NativeIDsFromProperties: arnUnderKey("Arn"),
		TagsFromProperties:      tagsFromKey("Tags"),
	},
	{
		TFType:                  "aws_cloudwatch_event_rule",
		CloudFormationType:      "AWS::Events::Rule",
		Slug:                    "cloudwatch_event_rule",
		ImportIDFromIdentifier:  passthroughImportID,
		NameHintFromProperties:  nameOrIdentifier("Name"),
		NativeIDsFromProperties: arnUnderKey("Arn"),
		TagsFromProperties:      tagsFromKey("Tags"),
	},

	// =====================================================================
	// IAM — global types; ForGlobalCFN dedupes across regions
	// =====================================================================
	{
		TFType:                  "aws_iam_role",
		CloudFormationType:      "AWS::IAM::Role",
		Slug:                    "iam_role",
		IsGlobal:                true,
		ImportIDFromIdentifier:  passthroughImportID,
		NameHintFromProperties:  nameOrIdentifier("RoleName"),
		NativeIDsFromProperties: arnUnderKey("Arn"),
		TagsFromProperties:      tagsFromKey("Tags"),
	},
	{
		TFType:             "aws_iam_policy",
		CloudFormationType: "AWS::IAM::ManagedPolicy",
		Slug:               "iam_policy",
		IsGlobal:           true,
		// Identifier IS the full policy ARN (per arnRule.identityFullARN);
		// Terraform aws_iam_policy import takes the ARN — passthrough.
		ImportIDFromIdentifier: passthroughImportID,
		NameHintFromProperties: nameOrIdentifier("ManagedPolicyName"),
		NativeIDsFromProperties: func(identifier string, _ map[string]any) map[string]string {
			return map[string]string{"arn": identifier}
		},
		TagsFromProperties: tagsFromKey("Tags"),
	},

	// =====================================================================
	// KMS — regional
	// =====================================================================
	{
		TFType:                  "aws_kms_key",
		CloudFormationType:      "AWS::KMS::Key",
		Slug:                    "kms",
		ImportIDFromIdentifier:  passthroughImportID,
		NameHintFromProperties:  nameOrIdentifier("KeyId"),
		NativeIDsFromProperties: arnUnderKey("Arn"),
		TagsFromProperties:      tagsFromKey("Tags"),
	},

	// =====================================================================
	// Compute — Lambda
	// =====================================================================
	{
		TFType:                  "aws_lambda_function",
		CloudFormationType:      "AWS::Lambda::Function",
		Slug:                    "lambda",
		ImportIDFromIdentifier:  passthroughImportID,
		NameHintFromProperties:  nameOrIdentifier("FunctionName"),
		NativeIDsFromProperties: arnUnderKey("Arn"),
		TagsFromProperties:      tagsFromKey("Tags"),
	},

	// =====================================================================
	// Storage — S3 / DynamoDB / Secrets Manager
	// =====================================================================
	{
		TFType:             "aws_s3_bucket",
		CloudFormationType: "AWS::S3::Bucket",
		Slug:               "s3",
		// RGT returns per-region tagged buckets (the GetBucketLocation
		// per-bucket regionalization that the hand-rolled discoverer
		// did is now unnecessary). Identifier = bucket name.
		ImportIDFromIdentifier:  passthroughImportID,
		NameHintFromProperties:  nameOrIdentifier("BucketName"),
		NativeIDsFromProperties: arnUnderKey("Arn"),
		TagsFromProperties:      tagsFromKey("Tags"),
	},
	{
		TFType:                  "aws_dynamodb_table",
		CloudFormationType:      "AWS::DynamoDB::Table",
		Slug:                    "dynamodb",
		ImportIDFromIdentifier:  passthroughImportID,
		NameHintFromProperties:  nameOrIdentifier("TableName"),
		NativeIDsFromProperties: arnUnderKey("Arn"),
		TagsFromProperties:      tagsFromKey("Tags"),
	},
	{
		TFType:             "aws_secretsmanager_secret",
		CloudFormationType: "AWS::SecretsManager::Secret",
		Slug:               "secretsmanager",
		// Identifier = secret ARN (full); Terraform import also takes
		// the ARN — passthrough.
		ImportIDFromIdentifier: passthroughImportID,
		NameHintFromProperties: nameOrIdentifier("Name"),
		NativeIDsFromProperties: func(identifier string, _ map[string]any) map[string]string {
			return map[string]string{"arn": identifier}
		},
		TagsFromProperties: tagsFromKey("Tags"),
	},

	// =====================================================================
	// VPC family — EC2
	// =====================================================================
	{
		TFType:                 "aws_vpc",
		CloudFormationType:     "AWS::EC2::VPC",
		Slug:                   "vpc",
		ImportIDFromIdentifier: passthroughImportID,
		NameHintFromProperties: passthroughIdentifierName,
		TagsFromProperties:     tagsFromKey("Tags"),
	},
	{
		TFType:                 "aws_subnet",
		CloudFormationType:     "AWS::EC2::Subnet",
		Slug:                   "subnet",
		ImportIDFromIdentifier: passthroughImportID,
		NameHintFromProperties: passthroughIdentifierName,
		TagsFromProperties:     tagsFromKey("Tags"),
	},
	{
		TFType:                 "aws_security_group",
		CloudFormationType:     "AWS::EC2::SecurityGroup",
		Slug:                   "security_group",
		ImportIDFromIdentifier: passthroughImportID,
		NameHintFromProperties: nameOrIdentifier("GroupName"),
		TagsFromProperties:     tagsFromKey("Tags"),
	},
	{
		TFType:                 "aws_internet_gateway",
		CloudFormationType:     "AWS::EC2::InternetGateway",
		Slug:                   "internet_gateway",
		ImportIDFromIdentifier: passthroughImportID,
		NameHintFromProperties: passthroughIdentifierName,
		TagsFromProperties:     tagsFromKey("Tags"),
	},
	{
		TFType:                 "aws_nat_gateway",
		CloudFormationType:     "AWS::EC2::NatGateway",
		Slug:                   "nat_gateway",
		ImportIDFromIdentifier: passthroughImportID,
		NameHintFromProperties: passthroughIdentifierName,
		TagsFromProperties:     tagsFromKey("Tags"),
	},
	{
		TFType:             "aws_eip",
		CloudFormationType: "AWS::EC2::EIP",
		Slug:               "eip",
		// Cloud Control identifier is compound "|<AllocationId>" (per
		// arnRule); Terraform import takes just the AllocationId. Strip
		// the leading "|".
		ImportIDFromIdentifier: func(identifier string, _ map[string]any) string {
			return strings.TrimPrefix(identifier, "|")
		},
		NameHintFromProperties: func(identifier string, _ map[string]any) string {
			return strings.TrimPrefix(identifier, "|")
		},
		NativeIDsFromProperties: func(identifier string, props map[string]any) map[string]string {
			out := map[string]string{}
			if ip := extractString(props, "PublicIp"); ip != "" {
				out["public_ip"] = ip
			}
			out["allocation_id"] = strings.TrimPrefix(identifier, "|")
			return out
		},
		TagsFromProperties: tagsFromKey("Tags"),
	},
	{
		TFType:                 "aws_route_table",
		CloudFormationType:     "AWS::EC2::RouteTable",
		Slug:                   "route_table",
		ImportIDFromIdentifier: passthroughImportID,
		NameHintFromProperties: passthroughIdentifierName,
		TagsFromProperties:     tagsFromKey("Tags"),
	},
	{
		TFType:                 "aws_network_acl",
		CloudFormationType:     "AWS::EC2::NetworkAcl",
		Slug:                   "network_acl",
		ImportIDFromIdentifier: passthroughImportID,
		NameHintFromProperties: passthroughIdentifierName,
		TagsFromProperties:     tagsFromKey("Tags"),
	},
	{
		TFType:                 "aws_vpc_endpoint",
		CloudFormationType:     "AWS::EC2::VPCEndpoint",
		Slug:                   "vpc_endpoint",
		ImportIDFromIdentifier: passthroughImportID,
		NameHintFromProperties: passthroughIdentifierName,
		TagsFromProperties:     tagsFromKey("Tags"),
	},
	{
		TFType:                 "aws_vpc_dhcp_options",
		CloudFormationType:     "AWS::EC2::DHCPOptions",
		Slug:                   "vpc_dhcp_options",
		ImportIDFromIdentifier: passthroughImportID,
		NameHintFromProperties: passthroughIdentifierName,
		TagsFromProperties:     tagsFromKey("Tags"),
	},
	{
		TFType:                 "aws_network_interface",
		CloudFormationType:     "AWS::EC2::NetworkInterface",
		Slug:                   "network_interface",
		ImportIDFromIdentifier: passthroughImportID,
		NameHintFromProperties: passthroughIdentifierName,
		TagsFromProperties:     tagsFromKey("Tags"),
	},

	// =====================================================================
	// Load balancing v2
	// =====================================================================
	{
		TFType:             "aws_lb",
		CloudFormationType: "AWS::ElasticLoadBalancingV2::LoadBalancer",
		Slug:               "lb",
		// Identifier = full ARN; Terraform import takes the ARN.
		ImportIDFromIdentifier: passthroughImportID,
		NameHintFromProperties: nameOrIdentifier("Name"),
		NativeIDsFromProperties: func(identifier string, _ map[string]any) map[string]string {
			return map[string]string{"arn": identifier}
		},
		TagsFromProperties: tagsFromKey("Tags"),
	},
	{
		TFType:                 "aws_lb_target_group",
		CloudFormationType:     "AWS::ElasticLoadBalancingV2::TargetGroup",
		Slug:                   "lb_target_group",
		ImportIDFromIdentifier: passthroughImportID,
		NameHintFromProperties: nameOrIdentifier("Name"),
		NativeIDsFromProperties: func(identifier string, _ map[string]any) map[string]string {
			return map[string]string{"arn": identifier}
		},
		TagsFromProperties: tagsFromKey("Tags"),
	},
	{
		TFType:             "aws_lb_listener",
		CloudFormationType: "AWS::ElasticLoadBalancingV2::Listener",
		Slug:               "lb_listener",
		// Listener identifier = full ARN. The hand-rolled parent-scoped
		// enumeration is no longer needed: RGT returns listener ARNs
		// directly.
		ImportIDFromIdentifier: passthroughImportID,
		NameHintFromProperties: func(identifier string, _ map[string]any) string {
			// Listener ARNs end in `:listener/app/<lb>/<lbId>/<listenerId>`;
			// the listenerId tail is the most useful human-friendly hint.
			if idx := strings.LastIndex(identifier, "/"); idx != -1 {
				return identifier[idx+1:]
			}
			return identifier
		},
		NativeIDsFromProperties: func(identifier string, _ map[string]any) map[string]string {
			return map[string]string{"arn": identifier}
		},
		// AWS::ElasticLoadBalancingV2::Listener.Tags doesn't exist in
		// the CFN schema (provider 5.x+: listeners ARE taggable but the
		// CFN schema lags). Returns nil; RGT tags take precedence on
		// the cache-hit path anyway. Caller's TagSelectors still match
		// against RGT-supplied tags.
		TagsFromProperties: nilTagsExtractor,
	},

	// =====================================================================
	// CDN / DNS — global types
	// =====================================================================
	{
		TFType:                 "aws_cloudfront_distribution",
		CloudFormationType:     "AWS::CloudFront::Distribution",
		Slug:                   "cloudfront_distribution",
		IsGlobal:               true,
		ImportIDFromIdentifier: passthroughImportID,
		NameHintFromProperties: passthroughIdentifierName,
		// Tags live under DistributionConfig in CFN, not flat.
		TagsFromProperties: func(props map[string]any) map[string]string {
			if cfg, ok := props["DistributionConfig"].(map[string]any); ok {
				return extractTagList(cfg, "Tags")
			}
			return nil
		},
	},
	{
		TFType:                 "aws_route53_zone",
		CloudFormationType:     "AWS::Route53::HostedZone",
		Slug:                   "route53_zone",
		IsGlobal:               true,
		ImportIDFromIdentifier: passthroughImportID,
		NameHintFromProperties: nameOrIdentifier("Name"),
		TagsFromProperties: func(props map[string]any) map[string]string {
			return extractTagList(props, "HostedZoneTags")
		},
	},

	// =====================================================================
	// RDS
	// =====================================================================
	{
		TFType:                  "aws_db_instance",
		CloudFormationType:      "AWS::RDS::DBInstance",
		Slug:                    "db_instance",
		ImportIDFromIdentifier:  passthroughImportID,
		NameHintFromProperties:  nameOrIdentifier("DBInstanceIdentifier"),
		NativeIDsFromProperties: arnUnderKey("DBInstanceArn"),
		TagsFromProperties:      tagsFromKey("Tags"),
	},
	{
		TFType:                 "aws_db_subnet_group",
		CloudFormationType:     "AWS::RDS::DBSubnetGroup",
		Slug:                   "db_subnet_group",
		ImportIDFromIdentifier: passthroughImportID,
		NameHintFromProperties: nameOrIdentifier("DBSubnetGroupName"),
		TagsFromProperties:     tagsFromKey("Tags"),
	},
	{
		TFType:                 "aws_db_parameter_group",
		CloudFormationType:     "AWS::RDS::DBParameterGroup",
		Slug:                   "db_parameter_group",
		ImportIDFromIdentifier: passthroughImportID,
		NameHintFromProperties: nameOrIdentifier("DBParameterGroupName"),
		TagsFromProperties:     tagsFromKey("Tags"),
	},

	// =====================================================================
	// API Gateway v2 (Api only — Stage stays hand-rolled per header)
	// =====================================================================
	{
		TFType:                 "aws_apigatewayv2_api",
		CloudFormationType:     "AWS::ApiGatewayV2::Api",
		Slug:                   "apigatewayv2_api",
		ImportIDFromIdentifier: passthroughImportID,
		NameHintFromProperties: nameOrIdentifier("Name"),
		// AWS::ApiGatewayV2::Api.Tags is a flat map[string]string in
		// the CFN schema (not a Key/Value list).
		TagsFromProperties: func(props map[string]any) map[string]string {
			return extractStringMap(props, "Tags")
		},
	},

	// =====================================================================
	// Cognito
	// =====================================================================
	{
		TFType:                  "aws_cognito_user_pool",
		CloudFormationType:      "AWS::Cognito::UserPool",
		Slug:                    "cognito_user_pool",
		ImportIDFromIdentifier:  passthroughImportID,
		NameHintFromProperties:  nameOrIdentifier("UserPoolName"),
		NativeIDsFromProperties: arnUnderKey("Arn"),
		// AWS::Cognito::UserPool surfaces tags as a flat string map
		// under `UserPoolTags` (verified live), NOT the Key/Value
		// list shape `Tags` uses for other types. Wrong extractor →
		// silently empty tags.
		TagsFromProperties: func(props map[string]any) map[string]string {
			return extractStringMap(props, "UserPoolTags")
		},
	},

	// =====================================================================
	// IAM Instance Profile — global, untaggable
	// =====================================================================
	{
		// AWS::IAM::InstanceProfile — untaggable (no Tags property on
		// the CFN schema and RGT doesn't surface instance profiles).
		// SkipProjectTagFilter bypasses the legacy Project filter for
		// the same reason as aws_backup_selection: the tag bag is
		// always empty by design.
		TFType:                  "aws_iam_instance_profile",
		CloudFormationType:      "AWS::IAM::InstanceProfile",
		Slug:                    "iam_instance_profile",
		IsGlobal:                true,
		SkipProjectTagFilter:    true,
		ImportIDFromIdentifier:  passthroughImportID,
		NameHintFromProperties:  nameOrIdentifier("InstanceProfileName"),
		NativeIDsFromProperties: arnUnderKey("Arn"),
		TagsFromProperties:      emptyTagsExtractor,
	},

	// =====================================================================
	// Lambda Event Source Mapping
	// =====================================================================
	{
		TFType:                 "aws_lambda_event_source_mapping",
		CloudFormationType:     "AWS::Lambda::EventSourceMapping",
		Slug:                   "lambda_event_source_mapping",
		ImportIDFromIdentifier: passthroughImportID,
		// The CFN schema doesn't expose a stable "name" — fall back to
		// FunctionName (the human-readable side) when present, then
		// the UUID identifier.
		NameHintFromProperties: func(identifier string, props map[string]any) string {
			if name := extractString(props, "FunctionName"); name != "" {
				return name
			}
			return identifier
		},
		NativeIDsFromProperties: arnUnderKey("EventSourceArn"),
		TagsFromProperties:      tagsFromKey("Tags"),
	},

	// =====================================================================
	// SSM Parameter
	// =====================================================================
	{
		// Cloud Control identifier for AWS::SSM::Parameter is the full
		// parameter name including the leading `/` (e.g. `/myapp/db`).
		// Terraform import takes the same identifier — passthrough.
		TFType:                 "aws_ssm_parameter",
		CloudFormationType:     "AWS::SSM::Parameter",
		Slug:                   "ssm_parameter",
		ImportIDFromIdentifier: passthroughImportID,
		NameHintFromProperties: nameOrIdentifier("Name"),
		// The CFN schema doesn't expose an ARN for SSM parameters;
		// the parameter name is the canonical native identifier.
		NativeIDsFromProperties: func(identifier string, _ map[string]any) map[string]string {
			return map[string]string{"name": identifier}
		},
		TagsFromProperties: tagsFromKey("Tags"),
	},

	// =====================================================================
	// OpenSearch Serverless
	// =====================================================================
	{
		TFType:                  "aws_opensearchserverless_collection",
		CloudFormationType:      "AWS::OpenSearchServerless::Collection",
		Slug:                    "opensearchserverless_collection",
		ImportIDFromIdentifier:  passthroughImportID,
		NameHintFromProperties:  nameOrIdentifier("Name"),
		NativeIDsFromProperties: arnUnderKey("Arn"),
		TagsFromProperties:      tagsFromKey("Tags"),
	},

	// =====================================================================
	// EKS Pod Identity Association — compound import-ID rewrite
	// =====================================================================
	{
		TFType:             "aws_eks_pod_identity_association",
		CloudFormationType: "AWS::EKS::PodIdentityAssociation",
		Slug:               "eks_pod_identity",
		// Cloud Control identifier = "cluster|assocID"; Terraform
		// import format = "cluster,assocID" (comma-separated). Rewrite.
		ImportIDFromIdentifier: func(identifier string, _ map[string]any) string {
			return strings.Replace(identifier, "|", ",", 1)
		},
		NameHintFromProperties: func(identifier string, props map[string]any) string {
			if name := extractString(props, "AssociationId"); name != "" {
				return name
			}
			// Fall back to the Cloud Control identifier's assoc-ID tail.
			if idx := strings.Index(identifier, "|"); idx != -1 {
				return identifier[idx+1:]
			}
			return identifier
		},
		TagsFromProperties: tagsFromKey("Tags"),
	},

	// =====================================================================
	// Cognito User Pool Client — parent-scoped on UserPoolId, untaggable
	// =====================================================================
	{
		// AWS::Cognito::UserPoolClient is parent-scoped: CC ListResources
		// requires ResourceModel={"UserPoolId":"..."}. The CFN schema has
		// no Tags property, so SkipProjectTagFilter bypasses the legacy
		// Project filter (matching the aws_iam_instance_profile precedent).
		TFType:               "aws_cognito_user_pool_client",
		CloudFormationType:   "AWS::Cognito::UserPoolClient",
		Slug:                 "cognito_user_pool_client",
		SkipProjectTagFilter: true,
		ParentLister:         listCognitoUserPools,
		// Cloud Control identifier = "<UserPoolId>|<ClientId>"; Terraform
		// import format = "<UserPoolId>/<ClientId>" (forward-slash).
		// Verified against terraform-provider-aws v6.x docs.
		ImportIDFromIdentifier: func(identifier string, _ map[string]any) string {
			return strings.Replace(identifier, "|", "/", 1)
		},
		NameHintFromProperties: nameOrIdentifier("ClientName"),
		NativeIDsFromProperties: func(identifier string, _ map[string]any) map[string]string {
			parts := strings.SplitN(identifier, "|", 2)
			if len(parts) != 2 {
				return nil
			}
			return map[string]string{
				"user_pool_id": parts[0],
				"client_id":    parts[1],
			}
		},
		TagsFromProperties: emptyTagsExtractor,
	},

	// =====================================================================
	// Lambda Alias — parent-scoped on FunctionName, untaggable
	// =====================================================================
	{
		// AWS::Lambda::Alias is parent-scoped on FunctionName. The CFN
		// schema has no Tags property; aliases inherit nothing from
		// their parent function for tagging purposes.
		TFType:               "aws_lambda_alias",
		CloudFormationType:   "AWS::Lambda::Alias",
		Slug:                 "lambda_alias",
		SkipProjectTagFilter: true,
		ParentLister:         listLambdaFunctions,
		// Cloud Control identifier = "<FunctionName>|<AliasName>";
		// Terraform import format = "<FunctionName>/<AliasName>".
		ImportIDFromIdentifier: func(identifier string, _ map[string]any) string {
			return strings.Replace(identifier, "|", "/", 1)
		},
		NameHintFromProperties: nameOrIdentifier("Name"),
		NativeIDsFromProperties: func(identifier string, props map[string]any) map[string]string {
			out := map[string]string{}
			if parts := strings.SplitN(identifier, "|", 2); len(parts) == 2 {
				out["function_name"] = parts[0]
				out["name"] = parts[1]
			}
			if arn := extractString(props, "AliasArn"); arn != "" {
				out["arn"] = arn
			}
			if len(out) == 0 {
				return nil
			}
			return out
		},
		TagsFromProperties: emptyTagsExtractor,
	},

	// =====================================================================
	// WAFv2 WebACL — parent-scoped on Scope (REGIONAL / CLOUDFRONT)
	// =====================================================================
	{
		// AWS::WAFv2::WebACL is parent-scoped on Scope. CLOUDFRONT scope
		// is only valid against the us-east-1 endpoint per AWS docs —
		// wafv2ParentModels returns REGIONAL only from other regions to
		// avoid InvalidRequestException.
		TFType:             "aws_wafv2_web_acl",
		CloudFormationType: "AWS::WAFv2::WebACL",
		Slug:               "wafv2_web_acl",
		ParentLister:       wafv2ParentModels,
		// Cloud Control identifier = "<Name>|<Id>|<Scope>"; Terraform
		// import format = "<Id>/<Name>/<Scope>" — different delimiter AND
		// reordered (Name and Id are swapped). Verified against
		// terraform-provider-aws v6.x docs.
		ImportIDFromIdentifier: func(identifier string, _ map[string]any) string {
			parts := strings.SplitN(identifier, "|", 3)
			if len(parts) != 3 {
				return identifier
			}
			return parts[1] + "/" + parts[0] + "/" + parts[2]
		},
		NameHintFromProperties:  nameOrIdentifier("Name"),
		NativeIDsFromProperties: arnUnderKey("Arn"),
		TagsFromProperties:      tagsFromKey("Tags"),
	},

	// =====================================================================
	// Cognito User Pool Domain — SDKLister-listed, untaggable
	// =====================================================================
	{
		// AWS::Cognito::UserPoolDomain's CC ListResources returns
		// UnsupportedActionException even though GetResource works.
		// The CC primary identifier is the compound
		// `<UserPoolId>|<Domain>` (per the CFN schema's
		// `primaryIdentifier: [/properties/UserPoolId,
		// /properties/Domain]`), NOT the bare Domain — emitting bare
		// Domain causes GetResource to return ValidationException
		// (see #421, post-merge live smoke of #412). The SDKLister
		// emits the compound shape; this config translates it back
		// down for Terraform's importer, which takes only the bare
		// Domain.
		TFType:               "aws_cognito_user_pool_domain",
		CloudFormationType:   "AWS::Cognito::UserPoolDomain",
		Slug:                 "cognito_user_pool_domain",
		SkipProjectTagFilter: true,
		SDKLister:            listCognitoUserPoolDomains,
		// Cloud Control identifier = "<UserPoolId>|<Domain>";
		// Terraform import format = "<Domain>" (bare). Strip the
		// `<UserPoolId>|` prefix.
		ImportIDFromIdentifier: func(identifier string, _ map[string]any) string {
			if _, domain, ok := strings.Cut(identifier, "|"); ok {
				return domain
			}
			return identifier
		},
		// NameHint is the Domain portion (the human-readable side
		// of the compound) when the identifier is well-formed,
		// falling back to the full identifier otherwise.
		NameHintFromProperties: func(identifier string, _ map[string]any) string {
			if _, domain, ok := strings.Cut(identifier, "|"); ok {
				return domain
			}
			return identifier
		},
		// NativeIDs split the compound identifier into structured
		// keys. Properties carry UserPoolId redundantly — prefer the
		// identifier-derived split for consistency and fall back to
		// the property only when the identifier is malformed (so
		// downstream readers always see SOME user_pool_id key).
		NativeIDsFromProperties: func(identifier string, props map[string]any) map[string]string {
			if poolID, domain, ok := strings.Cut(identifier, "|"); ok {
				return map[string]string{
					"user_pool_id": poolID,
					"domain":       domain,
				}
			}
			out := map[string]string{"domain": identifier}
			if id := extractString(props, "UserPoolId"); id != "" {
				out["user_pool_id"] = id
			}
			return out
		},
		TagsFromProperties: emptyTagsExtractor,
	},

	// =====================================================================
	// ACM Certificate — SDKLister-listed (CC LIST unsupported), taggable
	// =====================================================================
	{
		// AWS::CertificateManager::Certificate's CC ListResources
		// returns UnsupportedActionException; SDKLister enumerates via
		// acm:ListCertificates. CC GetResource is supported and is the
		// authoritative source for the properties payload (including
		// Tags as a Key/Value list).
		TFType:                  "aws_acm_certificate",
		CloudFormationType:      "AWS::CertificateManager::Certificate",
		Slug:                    "acm_certificate",
		SDKLister:               listACMCertificates,
		ImportIDFromIdentifier:  passthroughImportID,
		NameHintFromProperties:  nameOrIdentifier("DomainName"),
		NativeIDsFromProperties: arnUnderKey("Arn"),
		TagsFromProperties:      tagsFromKey("Tags"),
	},

	// =====================================================================
	// ApiGatewayV2 Route — parent-scoped on ApiId, untaggable
	// =====================================================================
	{
		// AWS::ApiGatewayV2::Route is parent-scoped: CC ListResources
		// requires ResourceModel={"ApiId":"..."}. The CFN schema has no
		// Tags property — SkipProjectTagFilter bypasses the Project
		// filter (tagging happens on the parent Api).
		TFType:               "aws_apigatewayv2_route",
		CloudFormationType:   "AWS::ApiGatewayV2::Route",
		Slug:                 "apigatewayv2_route",
		SkipProjectTagFilter: true,
		ParentLister:         listApigatewayv2Apis,
		// Cloud Control identifier = "<ApiId>|<RouteId>"; Terraform
		// import format = "<ApiId>/<RouteId>" (forward-slash).
		ImportIDFromIdentifier: func(identifier string, _ map[string]any) string {
			return strings.Replace(identifier, "|", "/", 1)
		},
		// RouteKey (e.g. "POST /signup") is the most human-readable
		// hint; fall back to the identifier when absent.
		NameHintFromProperties: nameOrIdentifier("RouteKey"),
		NativeIDsFromProperties: func(identifier string, _ map[string]any) map[string]string {
			parts := strings.SplitN(identifier, "|", 2)
			if len(parts) != 2 {
				return nil
			}
			return map[string]string{
				"api_id":   parts[0],
				"route_id": parts[1],
			}
		},
		TagsFromProperties: emptyTagsExtractor,
	},

	// =====================================================================
	// ApiGatewayV2 Integration — parent-scoped on ApiId, untaggable
	// =====================================================================
	{
		// AWS::ApiGatewayV2::Integration is parent-scoped on ApiId. No
		// Tags property in the CFN schema.
		TFType:               "aws_apigatewayv2_integration",
		CloudFormationType:   "AWS::ApiGatewayV2::Integration",
		Slug:                 "apigatewayv2_integration",
		SkipProjectTagFilter: true,
		ParentLister:         listApigatewayv2Apis,
		// Cloud Control identifier = "<ApiId>|<IntegrationId>";
		// Terraform import format = "<ApiId>/<IntegrationId>".
		ImportIDFromIdentifier: func(identifier string, _ map[string]any) string {
			return strings.Replace(identifier, "|", "/", 1)
		},
		// No stable "name" on this type — Description when present,
		// IntegrationType ("AWS_PROXY", "HTTP_PROXY", …) otherwise,
		// then the identifier.
		NameHintFromProperties: func(identifier string, props map[string]any) string {
			if name := extractString(props, "Description"); name != "" {
				return name
			}
			if name := extractString(props, "IntegrationType"); name != "" {
				return name
			}
			return identifier
		},
		NativeIDsFromProperties: func(identifier string, _ map[string]any) map[string]string {
			parts := strings.SplitN(identifier, "|", 2)
			if len(parts) != 2 {
				return nil
			}
			return map[string]string{
				"api_id":         parts[0],
				"integration_id": parts[1],
			}
		},
		TagsFromProperties: emptyTagsExtractor,
	},

	// =====================================================================
	// ApiGatewayV2 Authorizer — parent-scoped on ApiId, untaggable
	// =====================================================================
	{
		// AWS::ApiGatewayV2::Authorizer is parent-scoped on ApiId. No
		// Tags property in the CFN schema.
		TFType:               "aws_apigatewayv2_authorizer",
		CloudFormationType:   "AWS::ApiGatewayV2::Authorizer",
		Slug:                 "apigatewayv2_authorizer",
		SkipProjectTagFilter: true,
		ParentLister:         listApigatewayv2Apis,
		// Cloud Control identifier = "<ApiId>|<AuthorizerId>";
		// Terraform import format = "<ApiId>/<AuthorizerId>".
		ImportIDFromIdentifier: func(identifier string, _ map[string]any) string {
			return strings.Replace(identifier, "|", "/", 1)
		},
		NameHintFromProperties: nameOrIdentifier("Name"),
		NativeIDsFromProperties: func(identifier string, _ map[string]any) map[string]string {
			parts := strings.SplitN(identifier, "|", 2)
			if len(parts) != 2 {
				return nil
			}
			return map[string]string{
				"api_id":        parts[0],
				"authorizer_id": parts[1],
			}
		},
		TagsFromProperties: emptyTagsExtractor,
	},

	// =====================================================================
	// Cognito Identity Provider — parent-scoped on UserPoolId, untaggable
	// =====================================================================
	{
		// AWS::Cognito::UserPoolIdentityProvider is parent-scoped on
		// UserPoolId. No Tags property in the CFN schema.
		TFType:               "aws_cognito_identity_provider",
		CloudFormationType:   "AWS::Cognito::UserPoolIdentityProvider",
		Slug:                 "cognito_identity_provider",
		SkipProjectTagFilter: true,
		ParentLister:         listCognitoUserPools,
		// Cloud Control identifier = "<UserPoolId>|<ProviderName>";
		// Terraform import format = "<UserPoolId>:<ProviderName>"
		// (colon, NOT forward-slash — divergent from the other
		// compound-ID types in this file). Verified against
		// terraform-provider-aws v6.x docs for
		// aws_cognito_identity_provider.
		ImportIDFromIdentifier: func(identifier string, _ map[string]any) string {
			return strings.Replace(identifier, "|", ":", 1)
		},
		NameHintFromProperties: nameOrIdentifier("ProviderName"),
		NativeIDsFromProperties: func(identifier string, _ map[string]any) map[string]string {
			parts := strings.SplitN(identifier, "|", 2)
			if len(parts) != 2 {
				return nil
			}
			return map[string]string{
				"user_pool_id":  parts[0],
				"provider_name": parts[1],
			}
		},
		TagsFromProperties: emptyTagsExtractor,
	},

	// =====================================================================
	// Cognito Resource Server — parent-scoped on UserPoolId, untaggable
	// =====================================================================
	{
		// AWS::Cognito::UserPoolResourceServer is parent-scoped on
		// UserPoolId. No Tags property in the CFN schema.
		TFType:               "aws_cognito_resource_server",
		CloudFormationType:   "AWS::Cognito::UserPoolResourceServer",
		Slug:                 "cognito_resource_server",
		SkipProjectTagFilter: true,
		ParentLister:         listCognitoUserPools,
		// Cloud Control identifier = "<UserPoolId>|<Identifier>";
		// Terraform import format is the same pipe-delimited shape
		// (NOT rewritten to "/"). Verified against
		// terraform-provider-aws v6.x docs for
		// aws_cognito_resource_server.
		ImportIDFromIdentifier: passthroughImportID,
		// Resource Server "Name" is the human-readable display name;
		// "Identifier" is the OAuth scope namespace (also useful).
		NameHintFromProperties: func(identifier string, props map[string]any) string {
			if name := extractString(props, "Name"); name != "" {
				return name
			}
			if name := extractString(props, "Identifier"); name != "" {
				return name
			}
			return identifier
		},
		NativeIDsFromProperties: func(identifier string, _ map[string]any) map[string]string {
			parts := strings.SplitN(identifier, "|", 2)
			if len(parts) != 2 {
				return nil
			}
			return map[string]string{
				"user_pool_id": parts[0],
				"identifier":   parts[1],
			}
		},
		TagsFromProperties: emptyTagsExtractor,
	},

	// =====================================================================
	// Lambda Permission — parent-scoped on FunctionName, untaggable (#422)
	// =====================================================================
	{
		// AWS::Lambda::Permission is parent-scoped: CC ListResources
		// requires ResourceModel={"FunctionName":"..."}. The CFN schema
		// has no Tags property — SkipProjectTagFilter bypasses the
		// Project filter (the parent Lambda function carries tags).
		TFType:               "aws_lambda_permission",
		CloudFormationType:   "AWS::Lambda::Permission",
		Slug:                 "lambda_permission",
		SkipProjectTagFilter: true,
		ParentLister:         listLambdaFunctions,
		// Cloud Control identifier = "<FunctionName>|<Id>" (compound,
		// pipe-separated per the CC primaryIdentifier convention).
		// Terraform import format = "<FunctionName>/<Id>"
		// (forward-slash; verified against terraform-provider-aws v6.x
		// docs for aws_lambda_permission). First-`|`-only rewrite so
		// hypothetical pipes inside FunctionName (illegal per the
		// Lambda name regex but defended for symmetry with SplitN-cap-2
		// NativeIDs) survive verbatim past the first.
		ImportIDFromIdentifier: func(identifier string, _ map[string]any) string {
			return strings.Replace(identifier, "|", "/", 1)
		},
		// No "Name" on the Permission type — the StatementId (the
		// second half of the identifier) is the most human-readable
		// hint. We pull it out via the same SplitN that powers
		// NativeIDs rather than re-parsing the identifier.
		NameHintFromProperties: func(identifier string, _ map[string]any) string {
			if parts := strings.SplitN(identifier, "|", 2); len(parts) == 2 && parts[1] != "" {
				return parts[1]
			}
			return identifier
		},
		NativeIDsFromProperties: func(identifier string, _ map[string]any) map[string]string {
			parts := strings.SplitN(identifier, "|", 2)
			if len(parts) != 2 {
				return nil
			}
			return map[string]string{
				"function_name": parts[0],
				"statement_id":  parts[1],
			}
		},
		TagsFromProperties: emptyTagsExtractor,
	},

	// =====================================================================
	// Lambda Function URL — parent-scoped on TargetFunctionArn, untaggable (#422)
	// =====================================================================
	{
		// AWS::Lambda::Url is parent-scoped: CC ListResources requires
		// ResourceModel={"TargetFunctionArn":"..."} (NB: not
		// "FunctionName" — Lambda::Url uses the ARN where Permission /
		// Alias use the name. The lister listLambdaFunctionArns emits
		// the ARN-keyed model). CFN schema has no Tags property —
		// SkipProjectTagFilter bypasses the Project filter.
		TFType:               "aws_lambda_function_url",
		CloudFormationType:   "AWS::Lambda::Url",
		Slug:                 "lambda_function_url",
		SkipProjectTagFilter: true,
		ParentLister:         listLambdaFunctionArns,
		// Cloud Control primary identifier = "<FunctionArn>" (full
		// function ARN, single — the URL is uniquely keyed on the
		// associated function). Terraform's import format is the bare
		// function NAME (or "<name>/<qualifier>"), so the ARN must be
		// rewritten to the bare name. Lambda ARN shape:
		// arn:aws:lambda:<region>:<account>:function:<name>[:<qual>].
		// We extract the segment after "function:" and preserve any
		// qualifier as "<name>/<qualifier>" per the TF docs.
		ImportIDFromIdentifier: func(identifier string, _ map[string]any) string {
			const marker = "function:"
			idx := strings.Index(identifier, marker)
			if idx < 0 {
				// Already a bare name (or unparseable) — pass through.
				return identifier
			}
			rest := identifier[idx+len(marker):]
			// Rest is "<name>" or "<name>:<qualifier>". TF expects
			// "<name>" or "<name>/<qualifier>".
			if colon := strings.Index(rest, ":"); colon != -1 {
				return rest[:colon] + "/" + rest[colon+1:]
			}
			return rest
		},
		// Most readable name hint is the function name extracted from
		// the TargetFunctionArn (or the ARN identifier itself). Fall
		// back to the identifier when neither is parseable.
		NameHintFromProperties: func(identifier string, props map[string]any) string {
			if arn := extractString(props, "TargetFunctionArn"); arn != "" {
				if idx := strings.Index(arn, "function:"); idx >= 0 {
					rest := arn[idx+len("function:"):]
					if colon := strings.Index(rest, ":"); colon != -1 {
						return rest[:colon]
					}
					return rest
				}
				return arn
			}
			return identifier
		},
		NativeIDsFromProperties: func(identifier string, _ map[string]any) map[string]string {
			// identifier is the full FunctionArn — stamp under "arn"
			// for downstream callers indexing by ARN, and (when
			// recognizably ARN-shaped) extract the function_name for
			// the by-name native-id slot. Returning a non-nil empty
			// map when we can't extract a name (rather than nil) lets
			// callers always read out["arn"] safely; the bare-name
			// passthrough above means identifier may legitimately be
			// non-ARN-shaped on test/fixture inputs.
			out := map[string]string{"arn": identifier}
			if idx := strings.Index(identifier, "function:"); idx >= 0 {
				rest := identifier[idx+len("function:"):]
				if colon := strings.Index(rest, ":"); colon != -1 {
					out["function_name"] = rest[:colon]
				} else {
					out["function_name"] = rest
				}
			}
			return out
		},
		TagsFromProperties: emptyTagsExtractor,
	},

	// =====================================================================
	// ApiGateway v1 Stage — parent-scoped on RestApiId, TAGGABLE (#422)
	// =====================================================================
	{
		// AWS::ApiGateway::Stage is parent-scoped: CC ListResources
		// requires ResourceModel={"RestApiId":"..."}. Unlike the other
		// four #422 types, the CFN schema HAS a `Tags` property (array
		// of {Key,Value}) — taggable, no SkipProjectTagFilter, real
		// tagsFromKey extractor.
		TFType:             "aws_api_gateway_stage",
		CloudFormationType: "AWS::ApiGateway::Stage",
		Slug:               "api_gateway_stage",
		ParentLister:       listApigatewayRestAPIs,
		// Cloud Control identifier = "<RestApiId>|<StageName>";
		// Terraform import format = "<RestApiId>/<StageName>"
		// (forward-slash; verified against terraform-provider-aws v6.x
		// docs for aws_api_gateway_stage).
		ImportIDFromIdentifier: func(identifier string, _ map[string]any) string {
			return strings.Replace(identifier, "|", "/", 1)
		},
		NameHintFromProperties: nameOrIdentifier("StageName"),
		NativeIDsFromProperties: func(identifier string, _ map[string]any) map[string]string {
			parts := strings.SplitN(identifier, "|", 2)
			if len(parts) != 2 {
				return nil
			}
			return map[string]string{
				"rest_api_id": parts[0],
				"stage_name":  parts[1],
			}
		},
		TagsFromProperties: tagsFromKey("Tags"),
	},

	// =====================================================================
	// ApiGateway v1 Deployment — parent-scoped on RestApiId, untaggable (#422)
	// =====================================================================
	{
		// AWS::ApiGateway::Deployment is parent-scoped on RestApiId.
		// No Tags property in the CFN schema.
		//
		// IDENTIFIER ORDER DIVERGENCE: the CC primaryIdentifier is
		// ["/properties/DeploymentId", "/properties/RestApiId"] (note
		// order — DeploymentId FIRST, RestApiId second). That means
		// the CC compound identifier comes in as
		// "<DeploymentId>|<RestApiId>" but Terraform's import format
		// is "<RestApiId>/<DeploymentId>" — REVERSE order, not a naive
		// pipe→slash. Verified against terraform-provider-aws v6.x
		// docs. The rewriter below splits and re-stitches; the
		// extractor test pins this divergence to defend against a
		// "looks like every other compound type" rewrite regression.
		TFType:               "aws_api_gateway_deployment",
		CloudFormationType:   "AWS::ApiGateway::Deployment",
		Slug:                 "api_gateway_deployment",
		SkipProjectTagFilter: true,
		ParentLister:         listApigatewayRestAPIs,
		ImportIDFromIdentifier: func(identifier string, _ map[string]any) string {
			// CC: "<DeploymentId>|<RestApiId>" → TF: "<RestApiId>/<DeploymentId>".
			parts := strings.SplitN(identifier, "|", 2)
			if len(parts) != 2 {
				return identifier
			}
			return parts[1] + "/" + parts[0]
		},
		// No "Name" on Deployment — Description when present,
		// otherwise the identifier.
		NameHintFromProperties: func(identifier string, props map[string]any) string {
			if d := extractString(props, "Description"); d != "" {
				return d
			}
			return identifier
		},
		NativeIDsFromProperties: func(identifier string, _ map[string]any) map[string]string {
			parts := strings.SplitN(identifier, "|", 2)
			if len(parts) != 2 {
				return nil
			}
			// CC identifier order is DeploymentId|RestApiId.
			return map[string]string{
				"deployment_id": parts[0],
				"rest_api_id":   parts[1],
			}
		},
		TagsFromProperties: emptyTagsExtractor,
	},

	// =====================================================================
	// KMS Alias — SDKLister-listed, untaggable (#430)
	// =====================================================================
	{
		// AWS::KMS::Alias has CC list+read handlers but its CFN schema
		// declares taggable=false (KMS aliases don't carry tags — only
		// the underlying KMS keys do). Routing through SDKLister keeps
		// the discoverer's enumeration logic consistent with the ACM
		// certificate + cognito user pool domain precedents (#412) and
		// avoids relying on CC ListResources, which returns
		// per-account-AND-AWS-managed aliases mixed together; the
		// native kms:ListAliases is the canonical enumeration. The CC
		// primary identifier is the bare AliasName (e.g. "alias/foo")
		// — Terraform's import format is identical, so the rewriter is
		// a passthrough.
		TFType:                 "aws_kms_alias",
		CloudFormationType:     "AWS::KMS::Alias",
		Slug:                   "kms_alias",
		SkipProjectTagFilter:   true,
		SDKLister:              listKMSAliases,
		ImportIDFromIdentifier: passthroughImportID,
		NameHintFromProperties: nameOrIdentifier("AliasName"),
		// The CFN schema only exposes AliasName + TargetKeyId. There
		// is no ARN property; the alias name itself is the native ID.
		NativeIDsFromProperties: func(identifier string, props map[string]any) map[string]string {
			out := map[string]string{"name": identifier}
			if tk := extractString(props, "TargetKeyId"); tk != "" {
				out["target_key_id"] = tk
			}
			return out
		},
		TagsFromProperties: emptyTagsExtractor,
	},

	// =====================================================================
	// IAM User — SDKLister-listed, global, taggable (#430)
	// =====================================================================
	{
		// AWS::IAM::User is global; SDKLister uses iam:ListUsers (one
		// call, all users — IAM is a global service so the discoverer
		// runs once with region="" per the IsGlobal flag). CC primary
		// identifier = UserName; Terraform's import format for
		// aws_iam_user is also UserName — passthrough. CFN exposes
		// Tags as a list of {Key,Value} (the modern shape).
		TFType:                  "aws_iam_user",
		CloudFormationType:      "AWS::IAM::User",
		Slug:                    "iam_user",
		IsGlobal:                true,
		SDKLister:               listIAMUsers,
		ImportIDFromIdentifier:  passthroughImportID,
		NameHintFromProperties:  nameOrIdentifier("UserName"),
		NativeIDsFromProperties: arnUnderKey("Arn"),
		TagsFromProperties:      tagsFromKey("Tags"),
	},

	// =====================================================================
	// IAM Group — SDKLister-listed, global, untaggable (#430)
	// =====================================================================
	{
		// AWS::IAM::Group is global and explicitly untaggable per the
		// CFN schema (taggable=false; no Tags property at all). The CC
		// primary identifier = GroupName and Terraform's import format
		// matches — passthrough. SkipProjectTagFilter bypasses the
		// legacy Project filter for the same reason as
		// aws_iam_instance_profile / aws_backup_selection (the empty
		// tag bag would silently drop every group on --project scans).
		TFType:                  "aws_iam_group",
		CloudFormationType:      "AWS::IAM::Group",
		Slug:                    "iam_group",
		IsGlobal:                true,
		SkipProjectTagFilter:    true,
		SDKLister:               listIAMGroups,
		ImportIDFromIdentifier:  passthroughImportID,
		NameHintFromProperties:  nameOrIdentifier("GroupName"),
		NativeIDsFromProperties: arnUnderKey("Arn"),
		TagsFromProperties:      emptyTagsExtractor,
	},

	// =====================================================================
	// CloudFront Function — SDKLister-listed, global, CC vs TF id divergence (#430)
	// =====================================================================
	{
		// AWS::CloudFront::Function is global; the CC primary
		// identifier is the FUNCTION ARN
		// (arn:aws:cloudfront::<account>:function/<name>) but
		// Terraform's import format for aws_cloudfront_function is the
		// bare function NAME — rewriter strips the
		// "arn:aws:cloudfront::<acct>:function/" prefix.
		//
		// CFN declares the type taggable (Tags list of {Key,Value}),
		// but CC GetResource does not always include the Tags property
		// on AWS::CloudFront::Function in practice; downstream RGT
		// tags continue to cover the gap for the Project filter on the
		// taggable path. Mirrors the aws_acm_certificate shape (CC
		// returns the lightweight properties; tag-rich payload comes
		// from RGT).
		TFType:             "aws_cloudfront_function",
		CloudFormationType: "AWS::CloudFront::Function",
		Slug:               "cloudfront_function",
		IsGlobal:           true,
		SDKLister:          listCloudFrontFunctions,
		// CC identifier = "arn:aws:cloudfront::<acct>:function/<name>";
		// TF import format = bare "<name>". Extract the final path
		// segment; if the input isn't ARN-shaped fall through verbatim
		// so a malformed identifier surfaces clearly downstream rather
		// than getting silently mangled.
		ImportIDFromIdentifier: func(identifier string, _ map[string]any) string {
			const marker = ":function/"
			if idx := strings.Index(identifier, marker); idx >= 0 {
				return identifier[idx+len(marker):]
			}
			return identifier
		},
		NameHintFromProperties: func(identifier string, props map[string]any) string {
			if name := extractString(props, "Name"); name != "" {
				return name
			}
			// Fall back to the ARN tail (matches ImportIDFromIdentifier).
			const marker = ":function/"
			if idx := strings.Index(identifier, marker); idx >= 0 {
				return identifier[idx+len(marker):]
			}
			return identifier
		},
		NativeIDsFromProperties: func(identifier string, _ map[string]any) map[string]string {
			// Stamp the ARN under "arn" (the CC identifier IS the ARN)
			// and pull out the bare function name for the by-name slot.
			out := map[string]string{"arn": identifier}
			const marker = ":function/"
			if idx := strings.Index(identifier, marker); idx >= 0 {
				out["name"] = identifier[idx+len(marker):]
			}
			return out
		},
		TagsFromProperties: tagsFromKey("Tags"),
	},

	// =====================================================================
	// Secrets Manager Rotation Schedule — SDKLister-listed, untaggable (#430)
	// =====================================================================
	{
		// AWS::SecretsManager::RotationSchedule is the CFN sub-resource
		// modeling a secret's rotation configuration. Its CC primary
		// identifier is the parent secret's ARN (the `Id` property is
		// readOnly and equals the secret ARN) and Terraform's import
		// format for aws_secretsmanager_secret_rotation is also the
		// secret ARN — passthrough. CFN declares taggable=false
		// (rotation inherits from the parent secret for tag purposes).
		//
		// SDKLister filters Secrets Manager's ListSecrets output to
		// secrets with RotationEnabled=true so the GetResource fan-out
		// doesn't emit ResourceNotFoundException for every non-rotated
		// secret. SkipProjectTagFilter bypasses the Project filter
		// since rotation schedules are inherently tagless.
		TFType:                 "aws_secretsmanager_secret_rotation",
		CloudFormationType:     "AWS::SecretsManager::RotationSchedule",
		Slug:                   "secretsmanager_secret_rotation",
		SkipProjectTagFilter:   true,
		SDKLister:              listSecretsManagerSecretRotations,
		ImportIDFromIdentifier: passthroughImportID,
		NameHintFromProperties: func(identifier string, props map[string]any) string {
			// No "name" on the rotation schedule; pull the secret name
			// from the ARN tail when parseable. ARN shape:
			// arn:aws:secretsmanager:<region>:<account>:secret:<name>-<suffix>
			const marker = ":secret:"
			if idx := strings.Index(identifier, marker); idx >= 0 {
				return identifier[idx+len(marker):]
			}
			return identifier
		},
		NativeIDsFromProperties: func(identifier string, props map[string]any) map[string]string {
			out := map[string]string{"arn": identifier, "secret_id": identifier}
			if rl := extractString(props, "RotationLambdaARN"); rl != "" {
				out["rotation_lambda_arn"] = rl
			}
			return out
		},
		TagsFromProperties: emptyTagsExtractor,
	},

	// =====================================================================
	// ApiGateway v1 Resource — parent-scoped on RestApiId, untaggable (#422)
	// =====================================================================
	{
		// AWS::ApiGateway::Resource is parent-scoped on RestApiId. No
		// Tags property in the CFN schema.
		TFType:               "aws_api_gateway_resource",
		CloudFormationType:   "AWS::ApiGateway::Resource",
		Slug:                 "api_gateway_resource",
		SkipProjectTagFilter: true,
		ParentLister:         listApigatewayRestAPIs,
		// Cloud Control identifier = "<RestApiId>|<ResourceId>";
		// Terraform import format = "<RestApiId>/<ResourceId>"
		// (forward-slash). Verified against terraform-provider-aws
		// v6.x docs for aws_api_gateway_resource.
		ImportIDFromIdentifier: func(identifier string, _ map[string]any) string {
			return strings.Replace(identifier, "|", "/", 1)
		},
		// PathPart (e.g. "users", "{userId}") is the human-readable
		// hint; fall back to the identifier when absent.
		NameHintFromProperties: nameOrIdentifier("PathPart"),
		NativeIDsFromProperties: func(identifier string, _ map[string]any) map[string]string {
			parts := strings.SplitN(identifier, "|", 2)
			if len(parts) != 2 {
				return nil
			}
			return map[string]string{
				"rest_api_id": parts[0],
				"resource_id": parts[1],
			}
		},
		TagsFromProperties: emptyTagsExtractor,
	},

	// =====================================================================
	// ECS Cluster — CC default-list, taggable (#14f)
	// =====================================================================
	{
		// AWS::ECS::Cluster has standard CC list+read handlers. CC
		// primary identifier = ClusterName (the bare name) and
		// Terraform's import format is the same — passthrough.
		TFType:                  "aws_ecs_cluster",
		CloudFormationType:      "AWS::ECS::Cluster",
		Slug:                    "ecs_cluster",
		ImportIDFromIdentifier:  passthroughImportID,
		NameHintFromProperties:  nameOrIdentifier("ClusterName"),
		NativeIDsFromProperties: arnUnderKey("Arn"),
		TagsFromProperties:      tagsFromKey("Tags"),
	},

	// =====================================================================
	// EKS Cluster — SDKLister, taggable (#14f). Also seeds parent enumeration
	// for the four EKS child types.
	// =====================================================================
	{
		// AWS::EKS::Cluster has CC list+read handlers, but the same
		// SDK call (eks:ListClusters) seeds parent enumeration for
		// the four EKS child types (Nodegroup, Addon, FargateProfile,
		// AccessEntry). Routing the cluster type itself through
		// SDKLister keeps the EKS family consistent — every EKS lookup
		// in this bundle starts from a single eks:ListClusters call.
		// CC primary identifier = cluster Name and Terraform's import
		// format matches — passthrough.
		TFType:                  "aws_eks_cluster",
		CloudFormationType:      "AWS::EKS::Cluster",
		Slug:                    "eks_cluster",
		SDKLister:               listEKSClusters,
		ImportIDFromIdentifier:  passthroughImportID,
		NameHintFromProperties:  nameOrIdentifier("Name"),
		NativeIDsFromProperties: arnUnderKey("Arn"),
		TagsFromProperties:      tagsFromKey("Tags"),
	},

	// =====================================================================
	// EKS Node Group — parent-scoped on ClusterName, taggable (#14f)
	// =====================================================================
	{
		// AWS::EKS::Nodegroup is parent-scoped: CC ListResources
		// requires ResourceModel={"ClusterName":"..."}.
		//
		// Cloud Control identifier = "<ClusterName>|<NodegroupName>";
		// Terraform import format = "<ClusterName>:<NodegroupName>"
		// (colon — divergent from the typical pipe→slash rewrite).
		// Verified against terraform-provider-aws v6.x docs for
		// aws_eks_node_group: `id` is `<cluster_name>:<node_group_name>`.
		TFType:             "aws_eks_node_group",
		CloudFormationType: "AWS::EKS::Nodegroup",
		Slug:               "eks_node_group",
		ParentLister:       listEKSClustersAsResourceModels,
		ImportIDFromIdentifier: func(identifier string, _ map[string]any) string {
			return strings.Replace(identifier, "|", ":", 1)
		},
		NameHintFromProperties: nameOrIdentifier("NodegroupName"),
		NativeIDsFromProperties: func(identifier string, props map[string]any) map[string]string {
			parts := strings.SplitN(identifier, "|", 2)
			if len(parts) != 2 {
				return nil
			}
			out := map[string]string{
				"cluster_name":    parts[0],
				"node_group_name": parts[1],
			}
			if arn := extractString(props, "Arn"); arn != "" {
				out["arn"] = arn
			}
			return out
		},
		TagsFromProperties: tagsFromKey("Tags"),
	},

	// =====================================================================
	// EKS Addon — parent-scoped on ClusterName, taggable (#14f)
	// =====================================================================
	{
		// AWS::EKS::Addon is parent-scoped on ClusterName.
		//
		// Cloud Control identifier = "<ClusterName>|<AddonName>";
		// Terraform import format = "<ClusterName>:<AddonName>"
		// (colon). Verified against terraform-provider-aws v6.x docs
		// for aws_eks_addon.
		TFType:             "aws_eks_addon",
		CloudFormationType: "AWS::EKS::Addon",
		Slug:               "eks_addon",
		ParentLister:       listEKSClustersAsResourceModels,
		ImportIDFromIdentifier: func(identifier string, _ map[string]any) string {
			return strings.Replace(identifier, "|", ":", 1)
		},
		NameHintFromProperties: nameOrIdentifier("AddonName"),
		NativeIDsFromProperties: func(identifier string, props map[string]any) map[string]string {
			parts := strings.SplitN(identifier, "|", 2)
			if len(parts) != 2 {
				return nil
			}
			out := map[string]string{
				"cluster_name": parts[0],
				"addon_name":   parts[1],
			}
			if arn := extractString(props, "Arn"); arn != "" {
				out["arn"] = arn
			}
			return out
		},
		TagsFromProperties: tagsFromKey("Tags"),
	},

	// =====================================================================
	// EKS Fargate Profile — parent-scoped on ClusterName, taggable (#14f)
	// =====================================================================
	{
		// AWS::EKS::FargateProfile is parent-scoped on ClusterName.
		//
		// Cloud Control identifier = "<ClusterName>|<FargateProfileName>";
		// Terraform import format = "<ClusterName>/<FargateProfileName>"
		// (forward-slash — divergent from the sibling EKS child types
		// that use colon). Verified against terraform-provider-aws
		// v6.x docs for aws_eks_fargate_profile.
		TFType:             "aws_eks_fargate_profile",
		CloudFormationType: "AWS::EKS::FargateProfile",
		Slug:               "eks_fargate_profile",
		ParentLister:       listEKSClustersAsResourceModels,
		ImportIDFromIdentifier: func(identifier string, _ map[string]any) string {
			return strings.Replace(identifier, "|", "/", 1)
		},
		NameHintFromProperties: nameOrIdentifier("FargateProfileName"),
		NativeIDsFromProperties: func(identifier string, props map[string]any) map[string]string {
			parts := strings.SplitN(identifier, "|", 2)
			if len(parts) != 2 {
				return nil
			}
			out := map[string]string{
				"cluster_name":         parts[0],
				"fargate_profile_name": parts[1],
			}
			if arn := extractString(props, "Arn"); arn != "" {
				out["arn"] = arn
			}
			return out
		},
		TagsFromProperties: tagsFromKey("Tags"),
	},

	// =====================================================================
	// EKS Access Entry — parent-scoped on ClusterName, taggable (#14f)
	// =====================================================================
	{
		// AWS::EKS::AccessEntry is parent-scoped on ClusterName.
		//
		// Cloud Control identifier = "<ClusterName>|<PrincipalArn>";
		// Terraform import format = "<ClusterName>:<PrincipalArn>"
		// (colon). Verified against terraform-provider-aws v6.x docs
		// for aws_eks_access_entry. Note the PrincipalArn itself
		// contains colons (`arn:aws:iam::...`); the first-`|`-only
		// rewrite preserves them verbatim past the cluster boundary.
		TFType:             "aws_eks_access_entry",
		CloudFormationType: "AWS::EKS::AccessEntry",
		Slug:               "eks_access_entry",
		ParentLister:       listEKSClustersAsResourceModels,
		ImportIDFromIdentifier: func(identifier string, _ map[string]any) string {
			return strings.Replace(identifier, "|", ":", 1)
		},
		// No "name" on AccessEntry — PrincipalArn (the second half of
		// the compound id) is the most human-readable hint. Fall back
		// to the property when the identifier is malformed.
		NameHintFromProperties: func(identifier string, props map[string]any) string {
			if parts := strings.SplitN(identifier, "|", 2); len(parts) == 2 && parts[1] != "" {
				return parts[1]
			}
			if p := extractString(props, "PrincipalArn"); p != "" {
				return p
			}
			return identifier
		},
		NativeIDsFromProperties: func(identifier string, props map[string]any) map[string]string {
			parts := strings.SplitN(identifier, "|", 2)
			if len(parts) != 2 {
				return nil
			}
			out := map[string]string{
				"cluster_name":  parts[0],
				"principal_arn": parts[1],
			}
			if arn := extractString(props, "AccessEntryArn"); arn != "" {
				out["arn"] = arn
			}
			return out
		},
		TagsFromProperties: tagsFromKey("Tags"),
	},

	// =====================================================================
	// EC2 Instance — SDKLister, taggable (#14f)
	// =====================================================================
	{
		// AWS::EC2::Instance has CC list+read handlers, but typical
		// accounts carry hundreds of instances; the SDKLister path
		// uses ec2:DescribeInstances which filters out
		// terminated/shutting-down tombstones client-side (those CC
		// identifiers would surface ResourceNotFoundException on the
		// GetResource fan-out). CC primary identifier = InstanceId
		// and Terraform's import format matches — passthrough.
		TFType:                  "aws_instance",
		CloudFormationType:      "AWS::EC2::Instance",
		Slug:                    "instance",
		SDKLister:               listEC2Instances,
		ImportIDFromIdentifier:  passthroughImportID,
		NameHintFromProperties:  passthroughIdentifierName,
		NativeIDsFromProperties: arnUnderKey("Arn"),
		TagsFromProperties:      tagsFromKey("Tags"),
	},

	// =====================================================================
	// EC2 Launch Template — CC default-list, taggable (#14f)
	// =====================================================================
	{
		// AWS::EC2::LaunchTemplate has standard CC list+read handlers.
		// CC primary identifier = LaunchTemplateId (e.g. "lt-abc...")
		// and Terraform's import format matches — passthrough.
		// CFN exposes Tags as a flat list-of-Key/Value at the top
		// level (the resource also has a nested TagSpecifications
		// field that propagates tags to launched instances; that is a
		// distinct property and is not used here for resource-level
		// tag selectors).
		TFType:                  "aws_launch_template",
		CloudFormationType:      "AWS::EC2::LaunchTemplate",
		Slug:                    "launch_template",
		ImportIDFromIdentifier:  passthroughImportID,
		NameHintFromProperties:  nameOrIdentifier("LaunchTemplateName"),
		NativeIDsFromProperties: passthroughLaunchTemplateNativeIDs,
		TagsFromProperties:      tagsFromKey("Tags"),
	},

	// =====================================================================
	// Auto Scaling Group — SDKLister, taggable (#14f)
	// =====================================================================
	{
		// AWS::AutoScaling::AutoScalingGroup has CC list+read handlers
		// but the native autoscaling:DescribeAutoScalingGroups call
		// has clean pagination and the result naturally keys by
		// AutoScalingGroupName — matching the CC primary identifier
		// shape. Routing through SDKLister keeps the type aligned with
		// the rest of the #14f BYO compute bundle.
		//
		// CC primary identifier = AutoScalingGroupName (bare name)
		// and Terraform's import format matches — passthrough.
		// Tags use the standard Key/Value list shape; the
		// PropagateAtLaunch flag is ASG-specific and is not consumed
		// here (tag selectors operate on Key/Value alone).
		TFType:                  "aws_autoscaling_group",
		CloudFormationType:      "AWS::AutoScaling::AutoScalingGroup",
		Slug:                    "autoscaling_group",
		SDKLister:               listAutoScalingGroups,
		ImportIDFromIdentifier:  passthroughImportID,
		NameHintFromProperties:  nameOrIdentifier("AutoScalingGroupName"),
		NativeIDsFromProperties: arnUnderKey("AutoScalingGroupARN"),
		TagsFromProperties:      tagsFromKey("Tags"),
	},

	// =====================================================================
	// EC2 Key Pair — SDKLister, taggable (#14f)
	// =====================================================================
	{
		// AWS::EC2::KeyPair has CC list+read handlers; the SDKLister
		// path uses ec2:DescribeKeyPairs (one call, all key pairs in
		// the region — per-account key-pair counts are bounded by AWS
		// service quotas). CC primary identifier = KeyName and
		// Terraform's import format matches — passthrough.
		TFType:                  "aws_key_pair",
		CloudFormationType:      "AWS::EC2::KeyPair",
		Slug:                    "key_pair",
		SDKLister:               listEC2KeyPairs,
		ImportIDFromIdentifier:  passthroughImportID,
		NameHintFromProperties:  nameOrIdentifier("KeyName"),
		NativeIDsFromProperties: passthroughKeyPairNativeIDs,
		TagsFromProperties:      tagsFromKey("Tags"),
	},

	// =====================================================================
	// ElastiCache Replication Group — CC default-list, taggable (#14g)
	// =====================================================================
	{
		// AWS::ElastiCache::ReplicationGroup has standard CC list+read
		// handlers. CC primary identifier = ReplicationGroupId (the bare
		// name, e.g. "my-redis") and Terraform's import format for
		// aws_elasticache_replication_group matches — passthrough.
		// Verified against terraform-provider-aws v6.x docs and live
		// CC probe.
		TFType:                  "aws_elasticache_replication_group",
		CloudFormationType:      "AWS::ElastiCache::ReplicationGroup",
		Slug:                    "elasticache_replication_group",
		ImportIDFromIdentifier:  passthroughImportID,
		NameHintFromProperties:  nameOrIdentifier("ReplicationGroupId"),
		NativeIDsFromProperties: arnUnderKey("Arn"),
		TagsFromProperties:      tagsFromKey("Tags"),
	},

	// =====================================================================
	// ElastiCache Parameter Group — CC default-list, taggable (#14g)
	// =====================================================================
	{
		// AWS::ElastiCache::ParameterGroup has standard CC list+read
		// handlers. CC primary identifier = CacheParameterGroupName
		// (e.g. "default.redis7") and Terraform's import format for
		// aws_elasticache_parameter_group matches — passthrough. There
		// is no ARN on the CFN schema; the name itself is the canonical
		// native identifier.
		TFType:                 "aws_elasticache_parameter_group",
		CloudFormationType:     "AWS::ElastiCache::ParameterGroup",
		Slug:                   "elasticache_parameter_group",
		ImportIDFromIdentifier: passthroughImportID,
		NameHintFromProperties: nameOrIdentifier("CacheParameterGroupName"),
		NativeIDsFromProperties: func(identifier string, _ map[string]any) map[string]string {
			return map[string]string{"name": identifier}
		},
		TagsFromProperties: tagsFromKey("Tags"),
	},

	// =====================================================================
	// ElastiCache Subnet Group — CC default-list, taggable (#14g)
	// =====================================================================
	{
		// AWS::ElastiCache::SubnetGroup has standard CC list+read
		// handlers. CC primary identifier = CacheSubnetGroupName and
		// Terraform's import format for aws_elasticache_subnet_group
		// matches — passthrough. No ARN on the CFN schema.
		TFType:                 "aws_elasticache_subnet_group",
		CloudFormationType:     "AWS::ElastiCache::SubnetGroup",
		Slug:                   "elasticache_subnet_group",
		ImportIDFromIdentifier: passthroughImportID,
		NameHintFromProperties: nameOrIdentifier("CacheSubnetGroupName"),
		NativeIDsFromProperties: func(identifier string, _ map[string]any) map[string]string {
			return map[string]string{"name": identifier}
		},
		TagsFromProperties: tagsFromKey("Tags"),
	},

	// =====================================================================
	// MSK Cluster — CC default-list, taggable (#14g)
	// =====================================================================
	{
		// AWS::MSK::Cluster has standard CC list+read handlers. CC
		// primary identifier IS the cluster ARN (full
		// arn:aws:kafka:<region>:<acct>:cluster/<name>/<uuid>) and
		// Terraform's import format for aws_msk_cluster is also the
		// cluster ARN — passthrough.
		//
		// TAGS SHAPE DIVERGENCE: AWS::MSK::Cluster.Tags is a flat
		// map[string]string in the CFN schema (verified via
		// cloudformation:DescribeType — `type: object` with
		// patternProperties), NOT the Key/Value list shape that most
		// modern services use. extractStringMap is the right
		// extractor; tagsFromKey/extractTagList would silently return
		// nil/empty because it expects a `[]any` of `{Key, Value}`
		// objects. Mirrors the AWS::Cognito::UserPool /
		// AWS::ApiGatewayV2::Api precedent.
		TFType:             "aws_msk_cluster",
		CloudFormationType: "AWS::MSK::Cluster",
		Slug:               "msk_cluster",
		// Identifier = full cluster ARN.
		ImportIDFromIdentifier: passthroughImportID,
		// ClusterName is the human-readable hint; falls back to the
		// identifier (the ARN) when absent.
		NameHintFromProperties: nameOrIdentifier("ClusterName"),
		NativeIDsFromProperties: func(identifier string, _ map[string]any) map[string]string {
			return map[string]string{"arn": identifier}
		},
		TagsFromProperties: func(props map[string]any) map[string]string {
			return extractStringMap(props, "Tags")
		},
	},

	// =====================================================================
	// MSK Configuration — CC default-list, untaggable (#14g)
	// =====================================================================
	{
		// AWS::MSK::Configuration has standard CC list+read handlers
		// but the CFN schema declares NO Tags property at all
		// (configurations are tagless — the parent cluster carries the
		// tags). SkipProjectTagFilter bypasses the legacy Project filter
		// (the empty tag bag would silently drop every configuration on
		// --project scans, matching the aws_msk_configuration entry
		// already present in untaggableAWS / NON_TAGGABLE_AWS).
		//
		// CC primary identifier IS the configuration ARN (full
		// arn:aws:kafka:<region>:<acct>:configuration/<name>/<uuid>)
		// and Terraform's import format for aws_msk_configuration is
		// also the configuration ARN — passthrough.
		TFType:                 "aws_msk_configuration",
		CloudFormationType:     "AWS::MSK::Configuration",
		Slug:                   "msk_configuration",
		SkipProjectTagFilter:   true,
		ImportIDFromIdentifier: passthroughImportID,
		NameHintFromProperties: nameOrIdentifier("Name"),
		NativeIDsFromProperties: func(identifier string, _ map[string]any) map[string]string {
			return map[string]string{"arn": identifier}
		},
		TagsFromProperties: emptyTagsExtractor,
	},

	// =====================================================================
	// OpenSearch Domain — SDKLister-listed, taggable (#14g)
	// =====================================================================
	{
		// AWS::OpenSearchService::Domain's CC ListResources returns
		// UnsupportedActionException (verified via live probe). CC
		// GetResource is supported, so we enumerate via the native
		// opensearch:ListDomainNames SDK call and feed the resulting
		// DomainName values into the standard GetResource extractor
		// pipeline — mirrors the aws_acm_certificate / aws_kms_alias
		// precedents from #412 / #430.
		//
		// CC primary identifier = DomainName (e.g. "my-search") and
		// Terraform's import format for aws_opensearch_domain matches
		// — passthrough.
		TFType:                  "aws_opensearch_domain",
		CloudFormationType:      "AWS::OpenSearchService::Domain",
		Slug:                    "opensearch_domain",
		SDKLister:               listOpenSearchDomains,
		ImportIDFromIdentifier:  passthroughImportID,
		NameHintFromProperties:  nameOrIdentifier("DomainName"),
		NativeIDsFromProperties: arnUnderKey("DomainArn"),
		TagsFromProperties:      tagsFromKey("Tags"),
	},

	// =====================================================================
	// EBS Volume — CC default-list, taggable (#14g)
	// =====================================================================
	{
		// AWS::EC2::Volume has standard CC list+read handlers. CC
		// primary identifier = VolumeId (e.g. "vol-abc123") and
		// Terraform's import format for aws_ebs_volume matches —
		// passthrough. The CFN schema does not expose a top-level ARN
		// for volumes; the VolumeId itself is the canonical native
		// identifier.
		TFType:                 "aws_ebs_volume",
		CloudFormationType:     "AWS::EC2::Volume",
		Slug:                   "ebs_volume",
		ImportIDFromIdentifier: passthroughImportID,
		NameHintFromProperties: passthroughIdentifierName,
		NativeIDsFromProperties: func(identifier string, _ map[string]any) map[string]string {
			return map[string]string{"volume_id": identifier}
		},
		TagsFromProperties: tagsFromKey("Tags"),
	},
}

// passthroughImportID is the common ImportIDFromIdentifier used by every
// type whose Cloud Control primary identifier already matches Terraform's
// import format byte-for-byte.
func passthroughImportID(identifier string, _ map[string]any) string {
	return identifier
}

// passthroughIdentifierName is the common NameHintFromProperties used by
// EC2-family types whose CloudFormation schema has no explicit name
// field — the resource ID itself is the most human-readable hint.
func passthroughIdentifierName(identifier string, _ map[string]any) string {
	return identifier
}

// nameOrIdentifier returns a NameHintFromProperties extractor that reads
// the given properties key as a string and falls back to the identifier
// when the key is absent or empty.
func nameOrIdentifier(key string) func(identifier string, props map[string]any) string {
	return func(identifier string, props map[string]any) string {
		if name := extractString(props, key); name != "" {
			return name
		}
		return identifier
	}
}

// arnUnderKey returns a NativeIDsFromProperties extractor that stamps
// the given key's string value under the "arn" native-ID. Returns nil
// when the key is absent (so downstream sees "no native IDs" rather
// than an empty map).
func arnUnderKey(key string) func(identifier string, props map[string]any) map[string]string {
	return func(_ string, props map[string]any) map[string]string {
		arn := extractString(props, key)
		if arn == "" {
			return nil
		}
		return map[string]string{"arn": arn}
	}
}

// tagsFromKey returns a TagsFromProperties extractor that reads tags
// from the named properties key, treating them as a list of
// {Key, Value} objects — the CloudFormation v2 convention.
func tagsFromKey(key string) func(props map[string]any) map[string]string {
	return func(props map[string]any) map[string]string {
		return extractTagList(props, key)
	}
}

// nilTagsExtractor is the no-op TagsFromProperties for types whose
// CloudFormation schema does not surface tags (e.g.
// AWS::CloudWatch::Dashboard). Returns nil unconditionally so callers
// see "tags not fetched" rather than "empty tags".
func nilTagsExtractor(_ map[string]any) map[string]string {
	return nil
}

// emptyTagsExtractor returns a non-nil empty map for genuinely
// untaggable Cloud Control types (e.g. AWS::IAM::InstanceProfile,
// AWS::Backup::BackupSelection) whose CFN schema has no Tags property
// at all. Returning nil would break the #255 JSON-marshal contract
// (empty slice/map, not null) — downstream UIs gate panel rendering
// on that shape. Distinct from nilTagsExtractor, which is used for
// types whose tags simply weren't fetched in this code path.
func emptyTagsExtractor(_ map[string]any) map[string]string {
	return map[string]string{}
}

// passthroughLaunchTemplateNativeIDs builds the NativeIDs map for
// AWS::EC2::LaunchTemplate. The CC identifier IS the LaunchTemplateId
// (e.g. "lt-abc..."); the properties payload also surfaces
// LaunchTemplateName when set. We stamp both keys when present so
// downstream consumers can resolve a template by either handle. There
// is no top-level ARN on AWS::EC2::LaunchTemplate's CFN schema.
func passthroughLaunchTemplateNativeIDs(identifier string, props map[string]any) map[string]string {
	out := map[string]string{"id": identifier}
	if n := extractString(props, "LaunchTemplateName"); n != "" {
		out["name"] = n
	}
	return out
}

// passthroughKeyPairNativeIDs builds the NativeIDs map for
// AWS::EC2::KeyPair. The CC identifier IS the KeyName; the properties
// payload also surfaces KeyPairId (e.g. "key-abc...") and
// KeyFingerprint. We stamp the name + id pair so downstream consumers
// can resolve a key pair by either handle; KeyPair has no ARN.
func passthroughKeyPairNativeIDs(identifier string, props map[string]any) map[string]string {
	out := map[string]string{"name": identifier}
	if id := extractString(props, "KeyPairId"); id != "" {
		out["id"] = id
	}
	if fp := extractString(props, "KeyFingerprint"); fp != "" {
		out["fingerprint"] = fp
	}
	return out
}

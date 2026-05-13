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
		TFType:                 "aws_backup_vault",
		CloudFormationType:     "AWS::Backup::BackupVault",
		Slug:                   "backup_vault",
		ImportIDFromIdentifier: passthroughImportID,
		NameHintFromProperties: nameOrIdentifier("BackupVaultName"),
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
		TFType:                 "aws_cloudwatch_metric_alarm",
		CloudFormationType:     "AWS::CloudWatch::Alarm",
		Slug:                   "cloudwatch_metric_alarm",
		ImportIDFromIdentifier: passthroughImportID,
		NameHintFromProperties: nameOrIdentifier("AlarmName"),
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
		ImportIDFromIdentifier: passthroughImportID,
		NameHintFromProperties: nameOrIdentifier("LogGroupName"),
		NativeIDsFromProperties: arnUnderKey("Arn"),
		TagsFromProperties:      tagsFromKey("Tags"),
	},
	{
		TFType:                 "aws_cloudwatch_event_rule",
		CloudFormationType:     "AWS::Events::Rule",
		Slug:                   "cloudwatch_event_rule",
		ImportIDFromIdentifier: passthroughImportID,
		NameHintFromProperties: nameOrIdentifier("Name"),
		NativeIDsFromProperties: arnUnderKey("Arn"),
		TagsFromProperties:      tagsFromKey("Tags"),
	},

	// =====================================================================
	// IAM — global types; ForGlobalCFN dedupes across regions
	// =====================================================================
	{
		TFType:                 "aws_iam_role",
		CloudFormationType:     "AWS::IAM::Role",
		Slug:                   "iam_role",
		IsGlobal:               true,
		ImportIDFromIdentifier: passthroughImportID,
		NameHintFromProperties: nameOrIdentifier("RoleName"),
		NativeIDsFromProperties: arnUnderKey("Arn"),
		TagsFromProperties:      tagsFromKey("Tags"),
	},
	{
		TFType:                 "aws_iam_policy",
		CloudFormationType:     "AWS::IAM::ManagedPolicy",
		Slug:                   "iam_policy",
		IsGlobal:               true,
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
		TFType:                 "aws_kms_key",
		CloudFormationType:     "AWS::KMS::Key",
		Slug:                   "kms",
		ImportIDFromIdentifier: passthroughImportID,
		NameHintFromProperties: nameOrIdentifier("KeyId"),
		NativeIDsFromProperties: arnUnderKey("Arn"),
		TagsFromProperties:      tagsFromKey("Tags"),
	},

	// =====================================================================
	// Compute — Lambda
	// =====================================================================
	{
		TFType:                 "aws_lambda_function",
		CloudFormationType:     "AWS::Lambda::Function",
		Slug:                   "lambda",
		ImportIDFromIdentifier: passthroughImportID,
		NameHintFromProperties: nameOrIdentifier("FunctionName"),
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
		ImportIDFromIdentifier: passthroughImportID,
		NameHintFromProperties: nameOrIdentifier("BucketName"),
		NativeIDsFromProperties: arnUnderKey("Arn"),
		TagsFromProperties:      tagsFromKey("Tags"),
	},
	{
		TFType:                 "aws_dynamodb_table",
		CloudFormationType:     "AWS::DynamoDB::Table",
		Slug:                   "dynamodb",
		ImportIDFromIdentifier: passthroughImportID,
		NameHintFromProperties: nameOrIdentifier("TableName"),
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
		TFType:             "aws_lb_target_group",
		CloudFormationType: "AWS::ElasticLoadBalancingV2::TargetGroup",
		Slug:               "lb_target_group",
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
		TFType:                 "aws_db_instance",
		CloudFormationType:     "AWS::RDS::DBInstance",
		Slug:                   "db_instance",
		ImportIDFromIdentifier: passthroughImportID,
		NameHintFromProperties: nameOrIdentifier("DBInstanceIdentifier"),
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
	// OpenSearch Serverless
	// =====================================================================
	{
		TFType:                 "aws_opensearchserverless_collection",
		CloudFormationType:     "AWS::OpenSearchServerless::Collection",
		Slug:                   "opensearchserverless_collection",
		ImportIDFromIdentifier: passthroughImportID,
		NameHintFromProperties: nameOrIdentifier("Name"),
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

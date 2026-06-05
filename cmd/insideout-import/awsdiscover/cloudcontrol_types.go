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
		// format is `<BackupPlanId>|<SelectionId>` (pipe-separated, plan
		// FIRST — the reverse of the CC field order; verified against
		// terraform-provider-aws aws_backup_selection import docs). A
		// naive single `_`→`|` replace emits `<SelectionId>|<BackupPlanId>`
		// (wrong order), which terraform import rejects and the selection
		// silently drops with no_generated_config.
		TFType:               "aws_backup_selection",
		CloudFormationType:   "AWS::Backup::BackupSelection",
		Slug:                 "backup_selection",
		SkipProjectTagFilter: true,
		ImportIDFromIdentifier: func(identifier string, _ map[string]any) string {
			if idx := strings.Index(identifier, "_"); idx != -1 {
				selectionID, planID := identifier[:idx], identifier[idx+1:]
				return planID + "|" + selectionID
			}
			return identifier
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
		// Lift the topic's CMK (CFN `KmsMasterKeyId`) into NativeIDs so the
		// closure resolver recovers the sns→aws_kms_key edge (presets#733).
		NativeIDsFromProperties: fkNativeIDs(func(identifier string, _ map[string]any) map[string]string {
			return map[string]string{"arn": identifier}
		},
			fkRef{cfnProp: "KmsMasterKeyId", nativeKey: "kms_master_key_id"},
		),
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
		// Lift the queue's CMK (CFN `KmsMasterKeyId`) into NativeIDs so the
		// closure resolver recovers the sqs→aws_kms_key edge (presets#733).
		NativeIDsFromProperties: fkNativeIDs(func(_ string, props map[string]any) map[string]string {
			arn := extractString(props, "Arn")
			if arn == "" {
				return nil
			}
			return map[string]string{"arn": arn}
		},
			fkRef{cfnProp: "KmsMasterKeyId", nativeKey: "kms_master_key_id"},
		),
		TagsFromProperties: tagsFromKey("Tags"),
		// #501 Normalizer: CFN AWS::SQS::Queue uses primary-name
		// `QueueName` (TF: `name`), seconds-suffix-elided
		// `MessageRetentionPeriod` (TF: `message_retention_seconds`)
		// and `VisibilityTimeout` (TF: `visibility_timeout_seconds`),
		// and a list-of-{Key,Value} `Tags` shape (TF:
		// map[string]*Value[string]). The renames + tag-list flatten
		// land the values on the right Layer-1 fields after the
		// camelToSnake projection.
		Normalizer: chain(
			renameField("QueueName", "Name"),
			renameField("MessageRetentionPeriod", "MessageRetentionSeconds"),
			renameField("VisibilityTimeout", "VisibilityTimeoutSeconds"),
			flattenTagList("Tags"),
		),
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
		ImportIDFromIdentifier: passthroughImportID,
		NameHintFromProperties: nameOrIdentifier("LogGroupName"),
		// Lift the log group's CMK (CFN `KmsKeyId`, an ARN) into NativeIDs
		// so the closure resolver recovers the log_group→aws_kms_key edge
		// (presets#733).
		NativeIDsFromProperties: fkNativeIDs(arnUnderKey("Arn"),
			fkRef{cfnProp: "KmsKeyId", nativeKey: "kms_key_id"},
		),
		TagsFromProperties: tagsFromKey("Tags"),
		// #501/#502 Normalizer: CFN AWS::Logs::LogGroup uses primary-name
		// `LogGroupName` (TF: `name`), an `Arn` that includes the
		// trailing `:*` log-stream wildcard (TF strips it), and a
		// list-of-{Key,Value} `Tags` shape (TF: map shape). The
		// trailing `synthIDFromField("Name")` step copies the
		// post-rename `Name` value into `Id` so the generated `id`
		// field lands the same value the retired hand-rolled enricher
		// produced (TF state stores the log-group name as the
		// resource id).
		//
		// As of #502 the hand-rolled cloudwatch_log_group enricher is
		// retired and this generic Cloud Control + Normalizer path is
		// the production enricher for aws_cloudwatch_log_group.
		Normalizer: chain(
			renameField("LogGroupName", "Name"),
			synthIDFromField("Name"),
			trimARNStar("Arn"),
			flattenTagList("Tags"),
		),
	},
	{
		TFType:             "aws_cloudwatch_event_rule",
		CloudFormationType: "AWS::Events::Rule",
		Slug:               "cloudwatch_event_rule",
		// Cloud Control's primary identifier for AWS::Events::Rule is the
		// full ARN. Terraform's import format is `<event-bus-name>/<rule-name>`
		// (verified against terraform-provider-aws aws_cloudwatch_event_rule
		// import docs), NOT the ARN — passthrough emits the ARN and the rule
		// silently drops with no_generated_config. eventRuleImportID parses
		// the ARN's resource part (`rule/<bus>/<name>` for a custom bus,
		// `rule/<name>` for the implicit default bus) into the bus/name form.
		ImportIDFromIdentifier: func(identifier string, _ map[string]any) string {
			return eventRuleImportID(identifier)
		},
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
		// CC ListResources for AWS::IAM::ManagedPolicy returns both
		// customer-managed AND AWS-managed policies. AWS-managed
		// policies (ARN account field literally "aws") are not
		// customer-owned and must never be imported into customer
		// state — see isAWSManagedPolicyARN and #652.
		SkipIdentifier: isAWSManagedPolicyARN,
		// Normalizer: CFN AWS::IAM::ManagedPolicy surfaces the policy
		// document as a nested JSON object under `PolicyDocument`, but
		// Terraform's `aws_iam_policy.policy` is a REQUIRED JSON-encoded
		// *string*. Without the stringify+rename the required `policy`
		// argument is absent from the composed resource block and
		// `terraform plan` fails with "Missing required argument:
		// policy" (reliable #1621). renameField alone can't bridge it —
		// an object landing on a `*Value[string]` field is dropped.
		Normalizer: chain(
			jsonStringifyField("PolicyDocument", "Policy"),
		),
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
		// Surface KeyManager (AWS | CUSTOMER) into NativeIDs["key_manager"]
		// when the Cloud Control payload carries it, so the instance-level
		// importability classifier (imported.UnimportableReason, #709) can
		// grey out AWS-managed keys instead of offering them. AWS-managed
		// keys (e.g. the ACM default key, KeyManager == "AWS") are not
		// customer-owned and FAIL import: the provider's read calls
		// kms:GetKeyRotationStatus, which AWS-managed keys deny — so the key
		// silently drops with no_generated_config.
		//
		// LIMITATION: AWS::KMS::Key's CloudFormation schema does NOT list
		// KeyManager among its (read-only) properties, so a Cloud Control
		// GetResource payload may omit it. KeyManager is therefore the
		// closest available signal but not a guaranteed one. This is the
		// same posture as the service-managed-ENI classifier (#709): when
		// the discoverer can't surface the discriminator, the key is treated
		// as importable here and the reverse-import genconfig prune (#708)
		// remains the backstop. The exclusion cannot be done via
		// SkipIdentifier because that hook receives only the bare KeyId
		// (a UUID with no manager signal) before the GetResource fan-out.
		NativeIDsFromProperties: func(identifier string, props map[string]any) map[string]string {
			out := map[string]string{}
			if arn := extractString(props, "Arn"); arn != "" {
				out["arn"] = arn
			}
			if km := extractString(props, "KeyManager"); km != "" {
				out["key_manager"] = km
			}
			if len(out) == 0 {
				return nil
			}
			return out
		},
		TagsFromProperties: tagsFromKey("Tags"),
		// Resolve KeyManager at DISCOVER time via kms:DescribeKey. The CC
		// AWS::KMS::Key schema omits KeyManager (see the LIMITATION note
		// above), so the NativeIDsFromProperties extractor above can't set
		// it and the reverse-import / genconfig dry-run never runs the
		// AttributeEnricher that otherwise would. PostDiscover stamps
		// NativeIDs["key_manager"] so imported.UnimportableReason classifies
		// AWS-managed keys (KeyManager=AWS, e.g. the ACM/RDS/DynamoDB
		// default keys) into unsupported.json instead of letting them drop
		// as no_generated_config orphans (#cust3 item 1).
		PostDiscover: kmsKeyPostDiscover,
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
		// Lift the execution-role ARN (CFN `Role`) and CMK ARN (CFN
		// `KmsKeyArn`) into NativeIDs so the enrich-free closure resolver
		// recovers the lambda→aws_iam_role and lambda→aws_kms_key edges
		// reliable's picker shows today (presets#733). `Role` is a top-
		// level required property carrying the full role ARN; `KmsKeyArn`
		// is the optional CMK that encrypts env vars / snapshots.
		NativeIDsFromProperties: fkNativeIDs(arnUnderKey("Arn"),
			fkRef{cfnProp: "Role", nativeKey: "role_arn"},
			fkRef{cfnProp: "KmsKeyArn", nativeKey: "kms_key_arn"},
		),
		TagsFromProperties: tagsFromKey("Tags"),
		// Normalizer: CFN AWS::Lambda::Function models each singleton
		// nested config (Environment, TracingConfig, VpcConfig, …) as a
		// plain JSON *object*, but the Terraform provider exposes them as
		// nested *blocks* — the generated Layer-1 struct types each as a
		// slice (`[]AWSLambdaFunctionEnvironment`, `tf:"environment,blocks"`).
		// An object landing on a slice-typed field makes encoding/json
		// hard-fail, which aborts the WHOLE generated.UnmarshalAttrs call
		// and drops every attribute — so the imported aws_lambda_function
		// came back with empty Attrs and `terraform plan` then failed on
		// the missing required `function_name` / `role` args
		// (reliable #1620, sibling of presets #638/#639). wrapObjectAsList
		// rewrites each object into a one-element list so the typed
		// unmarshal succeeds and every scalar argument is captured too.
		//
		// verbatimMapField runs FIRST for Environment.Variables: the
		// Variables keys are environment-variable NAMES (operator data),
		// and the generic camelToSnake recursion would mangle them
		// (`LOG_LEVEL` → `log__level`). The verbatim marker opts that
		// sub-tree out of key renaming — same mechanism flattenTagList
		// uses for tag keys.
		Normalizer: chain(
			verbatimMapField("Environment", "Variables"),
			wrapObjectAsList("DeadLetterConfig"),
			wrapObjectAsList("Environment"),
			wrapObjectAsList("EphemeralStorage"),
			wrapObjectAsList("ImageConfig"),
			wrapObjectAsList("LoggingConfig"),
			wrapObjectAsList("SnapStart"),
			wrapObjectAsList("TracingConfig"),
			wrapObjectAsList("VpcConfig"),
		),
	},

	// =====================================================================
	// Storage — S3 / DynamoDB / Secrets Manager
	// =====================================================================
	{
		TFType:             "aws_s3_bucket",
		CloudFormationType: "AWS::S3::Bucket",
		Slug:               "s3",
		// S3 bucket ARNs (arn:aws:s3:::name) carry NO region, and
		// ListBuckets / RGT GetResources are account-global — every
		// scanned region returns the same buckets. Treating S3 as a
		// per-region type therefore emitted the same bucket once per
		// scan region, each stamped with the (wrong) scan region (#1860).
		// Mark it global FOR ENUMERATION: the discoverer scans once
		// (region="") and reads the deduped set via RGTCacheForGlobalCFN
		// (dedups by ARN), so each bucket appears exactly once. IsGlobal
		// keeps that single ListBuckets/RGT pass — it does NOT mean the
		// bucket lives in us-east-1. A bucket in us-west-2 / eu-central-1
		// is generated under a us-east-1 provider only if its Identity.Region
		// is left empty (reliable then backfills the session region,
		// us-east-1), and generate-config-out fails for it (#1860 follow-up).
		// The s3_bucket enricher promotes each bucket's TRUE region (from
		// HeadBucket BucketRegion) into Identity.Region so genconfig groups
		// every bucket into its real region dir. Identifier = bucket name.
		IsGlobal:                true,
		ImportIDFromIdentifier:  passthroughImportID,
		NameHintFromProperties:  nameOrIdentifier("BucketName"),
		NativeIDsFromProperties: arnUnderKey("Arn"),
		TagsFromProperties:      tagsFromKey("Tags"),
		// Resolve the bucket's TRUE region at DISCOVER time via
		// s3:GetBucketLocation and promote it into Identity.Region. S3 is
		// IsGlobal-enumerated (region-less), so without this a non-us-east-1
		// bucket lands under the us-east-1 provider in genconfig and fails
		// cross-region generate-config-out (#cust3 item 3). The s3_bucket
		// AttributeEnricher does the same promotion from HeadBucket, but the
		// reverse-import / genconfig dry-run never runs enrichment — so the
		// region must be set here too. PostDiscover soft-fails: a bucket
		// whose location can't be read is still emitted (region empty →
		// backfilled to the primary region, the prior behavior).
		PostDiscover: s3BucketPostDiscover,
		// #501 Normalizer: CFN AWS::S3::Bucket uses primary-name
		// `BucketName` (TF: `bucket`) and a list-of-{Key,Value}
		// `Tags` shape (TF: map shape). NOTE: a hand-rolled enricher
		// in byTypeEnricher currently overrides the Cloud Control
		// generic path for this type — see issue #493 / the s3
		// multi-overlay note. The normalizer is staged here so
		// Bucket C can compare the generic-path payload to the
		// hand-rolled output and decide whether to retire the
		// override.
		Normalizer: chain(
			renameField("BucketName", "Bucket"),
			flattenTagList("Tags"),
		),
	},
	{
		TFType:                 "aws_dynamodb_table",
		CloudFormationType:     "AWS::DynamoDB::Table",
		Slug:                   "dynamodb",
		ImportIDFromIdentifier: passthroughImportID,
		NameHintFromProperties: nameOrIdentifier("TableName"),
		// Lift the table's SSE CMK (CFN nested
		// `SSESpecification.KMSMasterKeyId`) into NativeIDs so the closure
		// resolver recovers the dynamodb→aws_kms_key edge (presets#733).
		NativeIDsFromProperties: fkNativeIDsNested(arnUnderKey("Arn"),
			"SSESpecification", "KMSMasterKeyId", "kms_key_arn"),
		TagsFromProperties: tagsFromKey("Tags"),
	},
	{
		TFType:             "aws_secretsmanager_secret",
		CloudFormationType: "AWS::SecretsManager::Secret",
		Slug:               "secretsmanager",
		// Identifier = secret ARN (full); Terraform import also takes
		// the ARN — passthrough.
		ImportIDFromIdentifier: passthroughImportID,
		NameHintFromProperties: nameOrIdentifier("Name"),
		// Lift the secret's CMK (CFN `KmsKeyId`) into NativeIDs so the
		// closure resolver recovers the secret→aws_kms_key edge (presets#733).
		NativeIDsFromProperties: fkNativeIDs(func(identifier string, _ map[string]any) map[string]string {
			return map[string]string{"arn": identifier}
		},
			fkRef{cfnProp: "KmsKeyId", nativeKey: "kms_key_id"},
		),
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
		TFType:                  "aws_subnet",
		CloudFormationType:      "AWS::EC2::Subnet",
		Slug:                    "subnet",
		ImportIDFromIdentifier:  passthroughImportID,
		NameHintFromProperties:  passthroughIdentifierName,
		NativeIDsFromProperties: vpcIDNativeIDs,
		TagsFromProperties:      tagsFromKey("Tags"),
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
		// AWS::EC2::SecurityGroupIngress — CFN schema has no Tags
		// property (verified via describe-type us-east-1: properties
		// = [Id, CidrIp, CidrIpv6, Description, FromPort, GroupId,
		// GroupName, IpProtocol, SourcePrefixListId, …]; primary
		// identifier = `/properties/Id` returning `sgr-XXXXX`).
		// SkipProjectTagFilter bypasses both the RGT cache short-
		// circuit (RGT may surface `ec2:security-group-rule/…` ARNs
		// but can't disambiguate ingress vs egress — they share the
		// `security-group-rule` ARN resource-type segment) and the
		// post-fetch Project-tag filter.
		//
		// Terraform import format is the bare `sgr-XXXXX` ID
		// (verified against terraform-provider-aws main:
		// website/docs/r/vpc_security_group_ingress_rule.html.markdown
		// — `terraform import aws_vpc_security_group_ingress_rule.example sgr-…`).
		// Passthrough ImportIDFromIdentifier.
		//
		// No arnRule for `ec2:security-group-rule` — the ARN
		// resource-type segment is identical for ingress and egress,
		// so we'd misroute half the time. SkipProjectTagFilter=true
		// makes the cache fallback path always run, so the missing
		// arnRule is correct rather than a gap.
		TFType:                 "aws_vpc_security_group_ingress_rule",
		CloudFormationType:     "AWS::EC2::SecurityGroupIngress",
		Slug:                   "vpc_security_group_ingress_rule",
		SkipProjectTagFilter:   true,
		ImportIDFromIdentifier: passthroughImportID,
		NameHintFromProperties: passthroughIdentifierName,
		NativeIDsFromProperties: func(identifier string, props map[string]any) map[string]string {
			out := map[string]string{"security_group_rule_id": identifier}
			if gid := extractString(props, "GroupId"); gid != "" {
				out["security_group_id"] = gid
			}
			return out
		},
		TagsFromProperties: emptyTagsExtractor,
	},
	{
		// AWS::EC2::SecurityGroupEgress — mirror of the ingress entry
		// above. CFN schema has no Tags property (verified via
		// describe-type us-east-1: properties = [CidrIp, CidrIpv6,
		// Description, FromPort, ToPort, IpProtocol,
		// DestinationSecurityGroupId, Id, DestinationPrefixListId,
		// GroupId]; primary identifier = `/properties/Id` returning
		// `sgr-XXXXX`).
		//
		// Terraform import format is the bare `sgr-XXXXX` ID
		// (verified against terraform-provider-aws main:
		// website/docs/r/vpc_security_group_egress_rule.html.markdown).
		// Passthrough ImportIDFromIdentifier.
		TFType:                 "aws_vpc_security_group_egress_rule",
		CloudFormationType:     "AWS::EC2::SecurityGroupEgress",
		Slug:                   "vpc_security_group_egress_rule",
		SkipProjectTagFilter:   true,
		ImportIDFromIdentifier: passthroughImportID,
		NameHintFromProperties: passthroughIdentifierName,
		NativeIDsFromProperties: func(identifier string, props map[string]any) map[string]string {
			out := map[string]string{"security_group_rule_id": identifier}
			if gid := extractString(props, "GroupId"); gid != "" {
				out["security_group_id"] = gid
			}
			return out
		},
		TagsFromProperties: emptyTagsExtractor,
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
		// Cloud Control's primary identifier for AWS::EC2::EIP is the
		// compound `<PublicIp>|<AllocationId>` (verified live, e.g.
		// `100.49.75.26|eipalloc-07d114af86fd5d1c3`). The arnRule path
		// yields the `|<AllocationId>` form (empty PublicIp). Terraform
		// import for aws_eip takes JUST the AllocationId, so take the
		// segment after the LAST `|` — that handles both the live
		// `<ip>|<alloc>` form and the ARN-rule `|<alloc>` form. A plain
		// TrimPrefix("|") is a no-op on the live form and would emit the
		// wrong ID (with the public IP), silently dropping the EIP with
		// no_generated_config.
		ImportIDFromIdentifier: func(identifier string, _ map[string]any) string {
			return eipAllocID(identifier)
		},
		NameHintFromProperties: func(identifier string, _ map[string]any) string {
			return eipAllocID(identifier)
		},
		NativeIDsFromProperties: func(identifier string, props map[string]any) map[string]string {
			out := map[string]string{}
			if ip := extractString(props, "PublicIp"); ip != "" {
				out["public_ip"] = ip
			}
			out["allocation_id"] = eipAllocID(identifier)
			return out
		},
		TagsFromProperties: tagsFromKey("Tags"),
	},
	{
		TFType:                  "aws_route_table",
		CloudFormationType:      "AWS::EC2::RouteTable",
		Slug:                    "route_table",
		ImportIDFromIdentifier:  passthroughImportID,
		NameHintFromProperties:  passthroughIdentifierName,
		NativeIDsFromProperties: vpcIDNativeIDs,
		TagsFromProperties:      tagsFromKey("Tags"),
	},
	{
		TFType:                 "aws_network_acl",
		CloudFormationType:     "AWS::EC2::NetworkAcl",
		Slug:                   "network_acl",
		ImportIDFromIdentifier: passthroughImportID,
		NameHintFromProperties: passthroughIdentifierName,
		TagsFromProperties:     tagsFromKey("Tags"),
		// Resolve IsDefault at DISCOVER time via ec2:DescribeNetworkAcls.
		// The CC AWS::EC2::NetworkAcl schema exposes only {VpcId, Id,
		// Tags} — no IsDefault — so the property extractor cannot tell a
		// VPC's DEFAULT NACL from a custom one. The AWS provider refuses
		// to import a default NACL as aws_network_acl ("use the
		// `aws_default_network_acl` resource instead"), so generate-config-out
		// emits no body and the default NACL silently drops as
		// no_generated_config. PostDiscover stamps NativeIDs["is_default"]
		// and re-types default NACLs to aws_default_network_acl (which
		// bodies cleanly under the same acl-… import id). Custom NACLs
		// stay aws_network_acl. See network_acl_post_discover.go.
		PostDiscover: networkACLPostDiscover,
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
		// Surface interface_type so the instance-level importability
		// classifier (imported.UnimportableReason, #709) can grey out
		// service/parent-managed ENIs (nat_gateway, vpc_endpoint, …) in the
		// wizard instead of offering them as importable. Absent-safe: when the
		// CloudControl payload omits InterfaceType the key is simply not set
		// and the ENI is treated as importable, with the genconfig prune (#708)
		// as the backstop.
		NativeIDsFromProperties: func(identifier string, props map[string]any) map[string]string {
			out := map[string]string{"id": identifier}
			if it := extractString(props, "InterfaceType"); it != "" {
				out["interface_type"] = it
			}
			return out
		},
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
		// AWS::ElasticLoadBalancingV2::Listener is parent-scoped: CC
		// ListResources requires ResourceModel={"LoadBalancerArn":"..."}.
		// RGT supplies listener ARNs directly on cache-hit, but on RGT
		// cache miss the fallback CC ListResources fires with no
		// ResourceModel and AWS rejects with InvalidRequestException
		// (HTTP 400). Same regression class as #616's
		// AWS::EKS::PodIdentityAssociation; surfaced by the live #616
		// full-scan integration test against the platform-test-admin
		// account in us-east-1.
		//
		// Listener identifier = full ARN; Terraform import is passthrough.
		TFType:                 "aws_lb_listener",
		CloudFormationType:     "AWS::ElasticLoadBalancingV2::Listener",
		Slug:                   "lb_listener",
		ParentLister:           listLoadBalancersAsResourceModels,
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
		// Also carries DBParameterGroupName as a reverse foreign key so
		// the #650 parent-instance resolver can link an
		// aws_db_parameter_group child back to the instance that uses
		// it (the parameter group's own model has no instance back-ref).
		// Additionally lift the storage CMK (CFN `KmsKeyId`, an ARN) into
		// NativeIDs so the closure resolver recovers the
		// db_instance→aws_kms_key edge (presets#733).
		NativeIDsFromProperties: fkNativeIDs(
			arnAndKey("DBInstanceArn", "DBParameterGroupName", "db_parameter_group"),
			fkRef{cfnProp: "KmsKeyId", nativeKey: "kms_key_id"},
		),
		TagsFromProperties: tagsFromKey("Tags"),
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
	// EKS Pod Identity Association — parent-scoped on ClusterName, taggable (#616)
	// =====================================================================
	{
		// AWS::EKS::PodIdentityAssociation is parent-scoped: CC ListResources
		// requires ResourceModel={"ClusterName":"..."}. Without ParentLister
		// the fallback CC ListResources fires with no ResourceModel and AWS
		// rejects with InvalidRequestException (HTTP 400). See #616 / live
		// repro against test account in us-east-1.
		//
		// Cloud Control identifier = "cluster|assocID"; Terraform import
		// format = "cluster,assocID" (comma-separated). Rewrite.
		TFType:             "aws_eks_pod_identity_association",
		CloudFormationType: "AWS::EKS::PodIdentityAssociation",
		Slug:               "eks_pod_identity",
		ParentLister:       listEKSClustersAsResourceModels,
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
		TFType:                 "aws_elasticache_replication_group",
		CloudFormationType:     "AWS::ElastiCache::ReplicationGroup",
		Slug:                   "elasticache_replication_group",
		ImportIDFromIdentifier: passthroughImportID,
		NameHintFromProperties: nameOrIdentifier("ReplicationGroupId"),
		// Also carries CacheParameterGroupName as a reverse foreign key
		// so the #650 parent-instance resolver can link an
		// aws_elasticache_parameter_group child back to the replication
		// group that uses it (the parameter group's own model has no
		// replication-group back-ref).
		NativeIDsFromProperties: arnAndKey("Arn", "CacheParameterGroupName", "cache_parameter_group"),
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

	// =====================================================================
	// S3 Bucket Policy — CC default-list, untaggable (#14h)
	// =====================================================================
	{
		// AWS::S3::BucketPolicy has standard CC list+read handlers (live
		// probe in .tmp/14h-cc-probe.txt confirms ListResources returns
		// results — one entry per bucket that has a policy attached).
		// One policy per bucket: CC primary identifier = Bucket (the
		// bucket name) and Terraform's import format for
		// aws_s3_bucket_policy is also the bucket name — passthrough.
		//
		// Bucket policies have no Tags property on the CFN schema (the
		// parent bucket carries the tags). SkipProjectTagFilter bypasses
		// the legacy Project filter so policies on project-tagged
		// buckets don't get silently dropped (matches the
		// untaggableAWS / NON_TAGGABLE_AWS allowlist entry for
		// aws_s3_bucket_policy).
		TFType:                 "aws_s3_bucket_policy",
		CloudFormationType:     "AWS::S3::BucketPolicy",
		Slug:                   "s3_bucket_policy",
		SkipProjectTagFilter:   true,
		ImportIDFromIdentifier: passthroughImportID,
		NameHintFromProperties: nameOrIdentifier("Bucket"),
		NativeIDsFromProperties: func(identifier string, _ map[string]any) map[string]string {
			return map[string]string{"bucket": identifier}
		},
		TagsFromProperties: emptyTagsExtractor,
	},

	// =====================================================================
	// CloudFront Origin Access Identity — CC default-list, untaggable (#14h)
	// =====================================================================
	{
		// AWS::CloudFront::CloudFrontOriginAccessIdentity has standard CC
		// list+read handlers (probe confirmed — though the test account
		// returned 0 OAIs, the type advertised LIST support without
		// error). CC primary identifier = Id (the OAI ID, e.g.
		// "E2QWRUHAPOMQZL") which is read-only / auto-assigned by
		// CloudFront. Terraform's import format for
		// aws_cloudfront_origin_access_identity is the bare OAI ID —
		// passthrough.
		//
		// OAIs are a CloudFront-global resource and the CFN schema has
		// no Tags property — they carry no tags at all. SkipProjectTag
		// is true so the legacy Project filter doesn't drop them, and
		// the Slug groups OAI events alongside other cloudfront types.
		TFType:                 "aws_cloudfront_origin_access_identity",
		CloudFormationType:     "AWS::CloudFront::CloudFrontOriginAccessIdentity",
		Slug:                   "cloudfront_origin_access_identity",
		IsGlobal:               true,
		SkipProjectTagFilter:   true,
		ImportIDFromIdentifier: passthroughImportID,
		// The CFN schema's only human-readable hint is the optional
		// Comment field on CloudFrontOriginAccessIdentityConfig; the
		// flat properties view exposes it under that nested path. Fall
		// back to the identifier (OAI ID) when absent.
		NameHintFromProperties: func(identifier string, props map[string]any) string {
			if cfg, ok := props["CloudFrontOriginAccessIdentityConfig"].(map[string]any); ok {
				if c := extractString(cfg, "Comment"); c != "" {
					return c
				}
			}
			return identifier
		},
		NativeIDsFromProperties: func(identifier string, props map[string]any) map[string]string {
			out := map[string]string{"id": identifier}
			if s := extractString(props, "S3CanonicalUserId"); s != "" {
				out["s3_canonical_user_id"] = s
			}
			return out
		},
		TagsFromProperties: emptyTagsExtractor,
	},

	// =====================================================================
	// CloudFront Monitoring Subscription — SDKLister, untaggable (#14h)
	// =====================================================================
	{
		// AWS::CloudFront::MonitoringSubscription's CC ListResources
		// returns UnsupportedActionException (verified via live probe).
		// CC GetResource IS supported and keyed by DistributionId — so
		// we enumerate distributions via cloudfront:ListDistributions
		// and feed the resulting DistributionId list into the standard
		// CC GetResource fan-out. Mirrors the
		// aws_secretsmanager_secret_rotation precedent from #430
		// (parent-resource enumeration via the native SDK to seed a CC
		// GetResource sub-resource lookup).
		//
		// Per-distribution: GetResource on a distribution that has no
		// monitoring subscription returns ResourceNotFoundException;
		// the discoverer's per-item soft-fail (ServiceWarn) handles it
		// without aborting the region scan. Distributions are
		// CloudFront-global, so this lister is region-agnostic.
		//
		// CC primary identifier = DistributionId (e.g. "E2QWRUHAPOMQZL")
		// and Terraform's import format for
		// aws_cloudfront_monitoring_subscription is also the bare
		// DistributionId — passthrough.
		//
		// No Tags property on the CFN schema (config-only sub-resource);
		// SkipProjectTagFilter + emptyTagsExtractor matches the
		// untaggableAWS / NON_TAGGABLE_AWS entry.
		TFType:                 "aws_cloudfront_monitoring_subscription",
		CloudFormationType:     "AWS::CloudFront::MonitoringSubscription",
		Slug:                   "cloudfront_monitoring_subscription",
		IsGlobal:               true,
		SkipProjectTagFilter:   true,
		SDKLister:              listCloudFrontDistributionIDs,
		ImportIDFromIdentifier: passthroughImportID,
		NameHintFromProperties: nameOrIdentifier("DistributionId"),
		NativeIDsFromProperties: func(identifier string, _ map[string]any) map[string]string {
			return map[string]string{"distribution_id": identifier}
		},
		TagsFromProperties: emptyTagsExtractor,
	},

	// =====================================================================
	// CloudWatch Logs Resource Policy — CC default-list, untaggable (#14h)
	// =====================================================================
	{
		// AWS::Logs::ResourcePolicy has standard CC list+read handlers
		// (probe confirmed — list returned [] on the test account but
		// the type advertised LIST support without error). CC primary
		// identifier = PolicyName and Terraform's import format for
		// aws_cloudwatch_log_resource_policy is also the bare
		// PolicyName — passthrough.
		//
		// Resource policies have no Tags property on the CFN schema —
		// they're policy documents, not taggable resources.
		// SkipProjectTagFilter + emptyTagsExtractor matches the
		// untaggableAWS / NON_TAGGABLE_AWS allowlist entry.
		TFType:                 "aws_cloudwatch_log_resource_policy",
		CloudFormationType:     "AWS::Logs::ResourcePolicy",
		Slug:                   "cloudwatch_log_resource_policy",
		SkipProjectTagFilter:   true,
		ImportIDFromIdentifier: passthroughImportID,
		NameHintFromProperties: nameOrIdentifier("PolicyName"),
		NativeIDsFromProperties: func(identifier string, _ map[string]any) map[string]string {
			return map[string]string{"policy_name": identifier}
		},
		TagsFromProperties: emptyTagsExtractor,
	},

	// =====================================================================
	// CloudWatch Logs Log Stream — ParentLister on LogGroupName, untaggable (#14h)
	// =====================================================================
	{
		// AWS::Logs::LogStream is parent-scoped on LogGroupName: CC
		// ListResources without a ResourceModel returns
		// InvalidRequestException ("Missing or invalid ResourceModel
		// property … Required property:  (#: required key
		// [LogGroupName] not found)"). Verified via live probe.
		// ParentLister enumerates log groups via
		// logs:DescribeLogGroups and emits one
		// ResourceModel={"LogGroupName":"…"} JSON-string per group; the
		// discoverer fans ListResources out once per parent.
		//
		// Cloud Control identifier = "<LogGroupName>|<LogStreamName>"
		// (compound, pipe-separated). Terraform's import format for
		// aws_cloudwatch_log_stream is "<log_group_name>:<log_stream_name>"
		// (colon-separated) per terraform-provider-aws v6.x docs — pin
		// the rewrite via a single-replace "|" -> ":". The first-`|`-
		// only rewrite preserves any pipe characters that might appear
		// in a stream name (rare but legal in the CloudWatch Logs API).
		//
		// No Tags property on the CFN schema (the parent log group
		// carries the tags); SkipProjectTagFilter + emptyTagsExtractor
		// matches the untaggableAWS / NON_TAGGABLE_AWS allowlist entry.
		TFType:               "aws_cloudwatch_log_stream",
		CloudFormationType:   "AWS::Logs::LogStream",
		Slug:                 "cloudwatch_log_stream",
		SkipProjectTagFilter: true,
		ParentLister:         listCloudWatchLogGroupsAsResourceModels,
		ImportIDFromIdentifier: func(identifier string, _ map[string]any) string {
			return strings.Replace(identifier, "|", ":", 1)
		},
		NameHintFromProperties: nameOrIdentifier("LogStreamName"),
		NativeIDsFromProperties: func(identifier string, _ map[string]any) map[string]string {
			parts := strings.SplitN(identifier, "|", 2)
			if len(parts) != 2 {
				return nil
			}
			return map[string]string{
				"log_group_name":  parts[0],
				"log_stream_name": parts[1],
			}
		},
		TagsFromProperties: emptyTagsExtractor,
	},

	// =====================================================================
	// IAM Service-Linked Role — SDKLister-listed, global, untaggable (#14i)
	// =====================================================================
	{
		// AWS::IAM::ServiceLinkedRole's CC ListResources returns
		// UnsupportedActionException — service-linked roles are auto-
		// created by AWS services on demand (e.g. ElastiCache,
		// AutoScaling), so there's no LIST handler. CC GetResource IS
		// supported and keyed by AWSServiceName (the canonical service
		// principal hostname, e.g. "elasticache.amazonaws.com"). The
		// SDKLister walks iam:ListRoles, filters by the
		// "/aws-service-role/" path prefix that AWS stamps on every SLR,
		// and emits the AWSServiceName extracted from the role's Path.
		// IAM is global; IsGlobal=true mirrors aws_iam_user / _group.
		//
		// CC primary identifier = AWSServiceName (the service hostname,
		// e.g. "elasticache.amazonaws.com"). Terraform's import format
		// for aws_iam_service_linked_role is the role ARN per
		// terraform-provider-aws v6.x docs. We use a CC->ARN rewrite
		// inside ImportIDFromIdentifier using the role's Path +
		// RoleName, sourced from the CC GetResource properties payload
		// (the AWSServiceName alone isn't enough to reconstruct the
		// full ARN since the actual role suffix varies by service).
		// When properties are missing (defensive: malformed CC
		// payload), fall through to the CC identifier verbatim — a
		// downstream import will then surface a clear "wrong format"
		// error rather than a silent mis-import.
		//
		// CFN declares the type as supporting Tags, but service-linked
		// roles are AWS-managed: customers cannot attach tags via the
		// IAM API (tag attempts return AccessDenied). SkipProjectTag
		// matches that reality. We use emptyTagsExtractor for the same
		// reason — surface a non-nil empty map per #255 contract.
		TFType:               "aws_iam_service_linked_role",
		CloudFormationType:   "AWS::IAM::ServiceLinkedRole",
		Slug:                 "iam_service_linked_role",
		IsGlobal:             true,
		SkipProjectTagFilter: true,
		SDKLister:            listIAMServiceLinkedRoleServiceNames,
		// CC identifier = AWSServiceName (e.g. "elasticache.amazonaws.com");
		// TF import format = role ARN
		// (arn:aws:iam::<acct>:role/aws-service-role/<service>/<RoleName>).
		// CC GetResource properties carry RoleName + Path; assemble
		// the ARN when present, otherwise fall through verbatim so a
		// malformed CC payload surfaces clearly downstream.
		ImportIDFromIdentifier: func(identifier string, props map[string]any) string {
			arn := extractString(props, "RoleArn")
			if arn != "" {
				return arn
			}
			return identifier
		},
		// NameHint: prefer the CFN-surfaced RoleName (it's the AWS-
		// assigned role suffix, e.g. "AWSServiceRoleForElastiCache"),
		// falling back to the AWSServiceName identifier.
		NameHintFromProperties: nameOrIdentifier("RoleName"),
		NativeIDsFromProperties: func(identifier string, props map[string]any) map[string]string {
			out := map[string]string{"aws_service_name": identifier}
			if arn := extractString(props, "RoleArn"); arn != "" {
				out["arn"] = arn
			}
			if name := extractString(props, "RoleName"); name != "" {
				out["role_name"] = name
			}
			return out
		},
		TagsFromProperties: emptyTagsExtractor,
	},

	// =====================================================================
	// API Gateway v2 — DomainName (#14j)
	// =====================================================================
	{
		// AWS::ApiGatewayV2::DomainName — top-level taggable type, CC
		// ListResources supported (no ParentLister needed). CC primary
		// identifier = DomainName (the customer-visible domain string),
		// same as the Terraform import format — passthrough.
		//
		// AWS::ApiGatewayV2::DomainName.Tags is a flat map[string]string
		// in the CFN schema (verified against the public CFN type schema
		// endpoint:
		//   https://schema.cloudformation.us-east-1.amazonaws.com/aws-apigatewayv2-domainname.json
		// `properties.Tags.type = "object"` with `patternProperties[".*"]`).
		// This matches the existing aws_apigatewayv2_api Tags shape — use
		// extractStringMap, NOT extractTagList.
		TFType:                  "aws_apigatewayv2_domain_name",
		CloudFormationType:      "AWS::ApiGatewayV2::DomainName",
		Slug:                    "apigatewayv2_domain_name",
		ImportIDFromIdentifier:  passthroughImportID,
		NameHintFromProperties:  nameOrIdentifier("DomainName"),
		NativeIDsFromProperties: passthroughDomainNameNativeIDs,
		TagsFromProperties: func(props map[string]any) map[string]string {
			return extractStringMap(props, "Tags")
		},
	},

	// =====================================================================
	// ECS Cluster Capacity Providers — passthrough on cluster name, untaggable (#14j)
	// =====================================================================
	{
		// AWS::ECS::ClusterCapacityProviderAssociations is the standalone
		// CFN resource that manages capacity-provider associations on an
		// existing ECS cluster — exactly mirrors the terraform-provider-aws
		// resource aws_ecs_cluster_capacity_providers. CC primary
		// identifier = Cluster (the cluster name, single-property primary
		// identifier per the CFN schema:
		//   https://schema.cloudformation.us-east-1.amazonaws.com/aws-ecs-clustercapacityproviderassociations.json
		// `primaryIdentifier: [/properties/Cluster]`). Terraform's import
		// format passes the cluster name through unchanged (verified
		// against terraform-provider-aws main internal/service/ecs/
		// cluster_capacity_providers.go — Importer uses
		// schema.ImportStatePassthroughContext and d.SetId(clusterName)
		// in the Create path). Passthrough.
		//
		// No Tags property on the CFN schema — capacity-provider
		// associations are a sub-resource of the parent ECS cluster and
		// inherit no tagging surface. SkipProjectTagFilter +
		// emptyTagsExtractor matches the untaggableAWS / NON_TAGGABLE_AWS
		// allowlist entry. No ARN rule: this resource has no ARN of its
		// own (the parent cluster's ARN routes to aws_ecs_cluster); the
		// cache-miss ListResources fallback handles discovery.
		TFType:                 "aws_ecs_cluster_capacity_providers",
		CloudFormationType:     "AWS::ECS::ClusterCapacityProviderAssociations",
		Slug:                   "ecs_cluster_capacity_providers",
		SkipProjectTagFilter:   true,
		ImportIDFromIdentifier: passthroughImportID,
		NameHintFromProperties: nameOrIdentifier("Cluster"),
		NativeIDsFromProperties: func(identifier string, _ map[string]any) map[string]string {
			return map[string]string{"cluster": identifier}
		},
		TagsFromProperties: emptyTagsExtractor,
	},

	// =====================================================================
	// SNS Topic Subscription — ARN-keyed, untaggable (#14j)
	// =====================================================================
	{
		// AWS::SNS::Subscription — top-level untaggable type, CC
		// ListResources supported. CC primary identifier = Arn (the
		// SubscriptionArn, full ARN form
		// "arn:aws:sns:<region>:<acct>:<topic-name>:<uuid>"), per the
		// CFN schema:
		//   https://schema.cloudformation.us-east-1.amazonaws.com/aws-sns-subscription.json
		// `primaryIdentifier: [/properties/Arn]`. Terraform's import
		// format also takes the SubscriptionArn (verified against
		// terraform-provider-aws main internal/service/sns/
		// topic_subscription.go — `@ArnIdentity` annotation, and the
		// Create path does `d.SetId(aws.ToString(output.SubscriptionArn))`).
		// Passthrough.
		//
		// No Tags property on the CFN schema — SNS Subscriptions inherit
		// no tagging surface (tags live on the parent topic).
		// SkipProjectTagFilter + emptyTagsExtractor matches the
		// untaggableAWS / NON_TAGGABLE_AWS allowlist entry. No ARN rule
		// in arn_rules.go because the SNS subscription ARN shape
		// `<topic-name>:<uuid>` collides with the bare SNS topic ARN
		// shape after parseARN splits — discriminating between them
		// requires per-segment shape analysis that isn't worth wiring
		// for a type that doesn't surface in RGT today; the cache-miss
		// ListResources fallback handles discovery cleanly.
		TFType:                  "aws_sns_topic_subscription",
		CloudFormationType:      "AWS::SNS::Subscription",
		Slug:                    "sns_topic_subscription",
		SkipProjectTagFilter:    true,
		ImportIDFromIdentifier:  passthroughImportID,
		NameHintFromProperties:  snsSubscriptionNameHint,
		NativeIDsFromProperties: snsSubscriptionNativeIDs,
		TagsFromProperties:      emptyTagsExtractor,
	},

	// =====================================================================
	// IAM RolePolicy — SDKLister-listed, global, untaggable (Phase A.2 / #466)
	// =====================================================================
	{
		// AWS::IAM::RolePolicy's CC ListResources returns
		// UnsupportedActionException — inline role policies live under a
		// parent IAM role rather than as top-level resources, so CC has
		// no LIST handler. CC GetResource IS supported and keyed on the
		// compound primary identifier [PolicyName, RoleName] (verified
		// against the public CFN schema:
		//   https://schema.cloudformation.us-east-1.amazonaws.com/aws-iam-rolepolicy.json
		// `primaryIdentifier: [/properties/PolicyName, /properties/RoleName]`).
		// IAM is global; IsGlobal=true mirrors aws_iam_service_linked_role
		// and the rest of the IAM bucket.
		//
		// The SDKLister walks iam:ListRoles (paginated) and, for each
		// non-SLR role, iam:ListRolePolicies (paginated). It emits the
		// CC compound identifier "<PolicyName>|<RoleName>" — the
		// framework joins compound primary-identifier parts with `|` in
		// the order declared by the schema.
		//
		// Terraform's import format for aws_iam_role_policy is
		// `<role_name>:<role_policy_name>` (verified against
		// terraform-provider-aws main website/docs/r/iam_role_policy.html.markdown
		// per the Import section:
		//   "% terraform import aws_iam_role_policy.example
		//    role_of_mypolicy_name:mypolicy_name"
		// ). The rewrite SWAPS the CC `<PolicyName>|<RoleName>` to the
		// TF `<RoleName>:<PolicyName>` form. When the identifier is
		// malformed (defensive — no `|`), fall through verbatim so a
		// downstream `terraform import` surfaces a clear "wrong format"
		// error rather than a silent mis-import.
		//
		// The CFN schema has no Tags property — inline role policies are
		// untaggable in AWS provider 6.x. SkipProjectTagFilter +
		// emptyTagsExtractor matches the existing untaggableAWS /
		// NON_TAGGABLE_AWS allowlist entry (which already lists this
		// type — only the SDKLister wiring is new in #466).
		//
		// No ARN rule in arn_rules.go: inline IAM policies have no ARN
		// of their own (only the parent role's ARN is reachable, and
		// that routes to aws_iam_role). Discovery is SDKLister-only.
		TFType:               "aws_iam_role_policy",
		CloudFormationType:   "AWS::IAM::RolePolicy",
		Slug:                 "iam_role_policy",
		IsGlobal:             true,
		SkipProjectTagFilter: true,
		SDKLister:            listIAMRolePolicyIdentifiers,
		// ImportID rewrite: CC `<PolicyName>|<RoleName>` → TF
		// `<RoleName>:<PolicyName>` (swap halves, join with `:`).
		ImportIDFromIdentifier: func(identifier string, _ map[string]any) string {
			parts := strings.SplitN(identifier, "|", 2)
			if len(parts) != 2 {
				return identifier
			}
			return parts[1] + ":" + parts[0]
		},
		// NameHint: prefer the CFN-surfaced PolicyName (the human-
		// meaningful inline-policy suffix), falling back to the compound
		// identifier verbatim. The CC properties payload echoes
		// PolicyName + RoleName on GetResource.
		NameHintFromProperties: nameOrIdentifier("PolicyName"),
		NativeIDsFromProperties: func(identifier string, _ map[string]any) map[string]string {
			parts := strings.SplitN(identifier, "|", 2)
			if len(parts) != 2 {
				// Defensive: a malformed identifier (no `|`) almost
				// certainly means an upstream bug — emit just the
				// policy-name half so downstream readers can spot the
				// drift rather than receive a half-populated map.
				return map[string]string{"policy_name": identifier}
			}
			return map[string]string{
				"policy_name": parts[0],
				"role_name":   parts[1],
			}
		},
		TagsFromProperties: emptyTagsExtractor,
	},

	// =====================================================================
	// OpenSearch Serverless AccessPolicy — SDKLister-listed, untaggable (Phase A.3 / #466)
	// =====================================================================
	{
		// AWS::OpenSearchServerless::AccessPolicy's CC ListResources
		// returns UnsupportedActionException — the service has no LIST
		// handler exposed through CloudControl, but CC GetResource IS
		// supported and keyed on the compound primary identifier [Type,
		// Name] (verified against the public CFN schema:
		//   https://schema.cloudformation.us-east-1.amazonaws.com/aws-opensearchserverless-accesspolicy.json
		// `primaryIdentifier: [/properties/Type, /properties/Name]`).
		//
		// The SDKLister calls aoss:ListAccessPolicies once per
		// AccessPolicyType (today only "data") and concatenates the
		// per-type Name + Type pairs into "<Type>|<Name>" — the
		// framework joins compound primary-identifier parts with `|` in
		// schema-declared order.
		//
		// Terraform's import format is `<name>/<type>` (verified
		// against terraform-provider-aws main
		// website/docs/r/opensearchserverless_access_policy.html.markdown
		// per the Import section:
		//   "% terraform import aws_opensearchserverless_access_policy.example
		//    example/data"
		// ). The rewrite SWAPS the CC `<Type>|<Name>` halves and joins
		// them with `/` to produce `<Name>/<Type>`. When the identifier
		// is malformed (defensive — no `|`), fall through verbatim so a
		// downstream `terraform import` surfaces a clear "wrong format"
		// error.
		//
		// The CFN schema has no Tags property — OSS access policies are
		// untaggable in AWS provider 6.x. SkipProjectTagFilter +
		// emptyTagsExtractor matches the existing untaggableAWS /
		// NON_TAGGABLE_AWS allowlist entry (which already lists this
		// type — only the SDKLister wiring is new in #466).
		//
		// No ARN rule in arn_rules.go: OSS access policies are not
		// surfaced via Resource Groups Tagging API today (the SDK has
		// no ListTagsForResource for access policies), so RGT cache
		// hits never apply; discovery is SDKLister-only.
		TFType:               "aws_opensearchserverless_access_policy",
		CloudFormationType:   "AWS::OpenSearchServerless::AccessPolicy",
		Slug:                 "opensearchserverless_access_policy",
		SkipProjectTagFilter: true,
		SDKLister:            listOSSAccessPolicyIdentifiers,
		// ImportID rewrite: CC `<Type>|<Name>` → TF `<Name>/<Type>`
		// (swap halves, join with `/`).
		ImportIDFromIdentifier: func(identifier string, _ map[string]any) string {
			parts := strings.SplitN(identifier, "|", 2)
			if len(parts) != 2 {
				return identifier
			}
			return parts[1] + "/" + parts[0]
		},
		// NameHint: prefer the Name from properties (the customer-
		// facing policy name); fall back to the identifier verbatim.
		NameHintFromProperties: nameOrIdentifier("Name"),
		NativeIDsFromProperties: func(identifier string, _ map[string]any) map[string]string {
			parts := strings.SplitN(identifier, "|", 2)
			if len(parts) != 2 {
				return map[string]string{"name": identifier}
			}
			return map[string]string{
				"type": parts[0],
				"name": parts[1],
			}
		},
		TagsFromProperties: emptyTagsExtractor,
	},

	// =====================================================================
	// OpenSearch Serverless SecurityPolicy — SDKLister-listed, untaggable (Phase A.4 / #466)
	// =====================================================================
	{
		// AWS::OpenSearchServerless::SecurityPolicy mirrors the access-
		// policy shape: CC ListResources returns
		// UnsupportedActionException, CC GetResource works on the
		// compound primary identifier [Type, Name] (verified against
		// the public CFN schema:
		//   https://schema.cloudformation.us-east-1.amazonaws.com/aws-opensearchserverless-securitypolicy.json
		// `primaryIdentifier: [/properties/Type, /properties/Name]`).
		//
		// The SDKLister calls aoss:ListSecurityPolicies once per
		// SecurityPolicyType ("encryption" and "network") and
		// concatenates per-type Name + Type pairs. Emits
		// "<Type>|<Name>" — framework joins compound primary-identifier
		// parts with `|` in schema-declared order.
		//
		// Terraform's import format is `<name>/<type>` (verified
		// against terraform-provider-aws main
		// website/docs/r/opensearchserverless_security_policy.html.markdown
		// per the Import section:
		//   "% terraform import aws_opensearchserverless_security_policy.example
		//    example/encryption"
		// ). The rewrite SWAPS the CC `<Type>|<Name>` halves and joins
		// them with `/` to produce `<Name>/<Type>`.
		//
		// Untaggable (CFN schema has no Tags property); existing
		// untaggableAWS / NON_TAGGABLE_AWS allowlist entry remains in
		// sync.
		TFType:               "aws_opensearchserverless_security_policy",
		CloudFormationType:   "AWS::OpenSearchServerless::SecurityPolicy",
		Slug:                 "opensearchserverless_security_policy",
		SkipProjectTagFilter: true,
		SDKLister:            listOSSSecurityPolicyIdentifiers,
		ImportIDFromIdentifier: func(identifier string, _ map[string]any) string {
			parts := strings.SplitN(identifier, "|", 2)
			if len(parts) != 2 {
				return identifier
			}
			return parts[1] + "/" + parts[0]
		},
		NameHintFromProperties: nameOrIdentifier("Name"),
		NativeIDsFromProperties: func(identifier string, _ map[string]any) map[string]string {
			parts := strings.SplitN(identifier, "|", 2)
			if len(parts) != 2 {
				return map[string]string{"name": identifier}
			}
			return map[string]string{
				"type": parts[0],
				"name": parts[1],
			}
		},
		TagsFromProperties: emptyTagsExtractor,
	},

	// =====================================================================
	// API Gateway V2 ApiMapping — SDKLister-listed, untaggable (Phase A.5 / #466)
	// =====================================================================
	{
		// AWS::ApiGatewayV2::ApiMapping's CC ListResources returns
		// UnsupportedActionException — API mappings are sub-resources of
		// a parent custom domain name, so CC has no LIST handler. CC
		// GetResource IS supported and keyed on the compound primary
		// identifier [ApiMappingId, DomainName] (verified against the
		// public CFN schema:
		//   https://schema.cloudformation.us-east-1.amazonaws.com/aws-apigatewayv2-apimapping.json
		// `primaryIdentifier: [/properties/ApiMappingId, /properties/DomainName]`).
		//
		// The SDKLister walks apigatewayv2:GetDomainNames (paginated)
		// and for each domain calls apigatewayv2:GetApiMappings
		// (paginated, requires DomainName). Emits
		// "<ApiMappingId>|<DomainName>" — framework joins compound
		// primary-identifier parts with `|` in schema-declared order.
		//
		// Terraform's import format is `<api_mapping_id>/<domain_name>`
		// (verified against terraform-provider-aws v6.x docs
		// website/docs/r/apigatewayv2_api_mapping.html.markdown — Import
		// section example: "1122334/ws-api.example.com"). The rewrite
		// is a single `|` → `/` swap because the CC identifier order
		// already matches the TF identifier order.
		//
		// The CFN schema has no Tags property — API mappings are
		// sub-resources of the parent domain (which carries the tags).
		// SkipProjectTagFilter + emptyTagsExtractor matches the existing
		// untaggableAWS / NON_TAGGABLE_AWS allowlist entry (which
		// already lists this type — only the SDKLister wiring is new in
		// #466).
		TFType:               "aws_apigatewayv2_api_mapping",
		CloudFormationType:   "AWS::ApiGatewayV2::ApiMapping",
		Slug:                 "apigatewayv2_api_mapping",
		SkipProjectTagFilter: true,
		SDKLister:            listAPIGatewayV2ApiMappingIdentifiers,
		// ImportID rewrite: CC `<ApiMappingId>|<DomainName>` → TF
		// `<ApiMappingId>/<DomainName>` (same halves, swap `|` for `/`).
		ImportIDFromIdentifier: func(identifier string, _ map[string]any) string {
			return strings.Replace(identifier, "|", "/", 1)
		},
		// NameHint: prefer the CFN-surfaced ApiMappingKey (the
		// human-facing URL prefix, e.g. "v1" or "" for the root
		// mapping). Empty-string ApiMappingKey is a valid AWS state
		// (root mapping), and nameOrIdentifier falls through to the
		// compound identifier in that case so the UI never renders an
		// empty NameHint.
		NameHintFromProperties: nameOrIdentifier("ApiMappingKey"),
		NativeIDsFromProperties: func(identifier string, _ map[string]any) map[string]string {
			parts := strings.SplitN(identifier, "|", 2)
			if len(parts) != 2 {
				return map[string]string{"api_mapping_id": identifier}
			}
			return map[string]string{
				"api_mapping_id": parts[0],
				"domain_name":    parts[1],
			}
		},
		TagsFromProperties: emptyTagsExtractor,
	},
}

// passthroughImportID is the common ImportIDFromIdentifier used by every
// type whose Cloud Control primary identifier already matches Terraform's
// import format byte-for-byte.
func passthroughImportID(identifier string, _ map[string]any) string {
	return identifier
}

// eipAllocID extracts the AllocationId from an AWS::EC2::EIP Cloud Control
// identifier. The live primary identifier is the compound
// `<PublicIp>|<AllocationId>` (e.g.
// `100.49.75.26|eipalloc-07d114af86fd5d1c3`); the arnRule path yields the
// `|<AllocationId>` form (empty PublicIp). Terraform import for aws_eip
// takes only the AllocationId, so return the segment after the LAST `|`.
// This is correct for both forms; an identifier with no `|` is returned
// unchanged.
func eipAllocID(identifier string) string {
	if i := strings.LastIndex(identifier, "|"); i >= 0 {
		return identifier[i+1:]
	}
	return identifier
}

// eventRuleImportID converts an AWS::Events::Rule Cloud Control identifier
// (the full ARN) into Terraform's `<event-bus-name>/<rule-name>` import
// format. EventBridge rule ARNs take two shapes:
//
//   - custom bus:  arn:aws:events:<region>:<acct>:rule/<bus>/<name>
//   - default bus: arn:aws:events:<region>:<acct>:rule/<name>
//
// The resource segment after the 5th `:` is `rule/<bus>/<name>` or
// `rule/<name>`. We emit `<bus>/<name>`, defaulting the bus to `default`
// when the ARN carries only the rule name (the implicit default bus). A
// string that is not an events-rule ARN is returned unchanged so an
// already-correct `<bus>/<name>` input passes through untouched.
func eventRuleImportID(identifier string) string {
	const arnPrefix = "arn:"
	if !strings.HasPrefix(identifier, arnPrefix) {
		return identifier
	}
	// ARN layout: arn:partition:service:region:account-id:resource…
	// Split into 6 parts so the resource segment (which itself contains
	// `/` and possibly `:`) stays intact.
	parts := strings.SplitN(identifier, ":", 6)
	if len(parts) != 6 {
		return identifier
	}
	resource := parts[5] // e.g. "rule/<bus>/<name>" or "rule/<name>"
	const resPrefix = "rule/"
	if !strings.HasPrefix(resource, resPrefix) {
		return identifier
	}
	rest := strings.TrimPrefix(resource, resPrefix)
	if idx := strings.Index(rest, "/"); idx != -1 {
		// rest = "<bus>/<name>" — already the target form.
		return rest
	}
	// rest = "<name>" — implicit default bus.
	return "default/" + rest
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

// awsManagedPolicyARNPrefixes are the IAM-managed-policy ARN prefixes
// across the standard, GovCloud, and China partitions. An AWS-managed
// policy ARN carries the literal account field "aws" (e.g.
// arn:aws:iam::aws:policy/AWSAccountUsageReportAccess); a customer-owned
// policy carries a real 12-digit account ID.
var awsManagedPolicyARNPrefixes = []string{
	"arn:aws:iam::aws:policy/",
	"arn:aws-us-gov:iam::aws:policy/",
	"arn:aws-cn:iam::aws:policy/",
}

// isAWSManagedPolicyARN reports whether arn is an AWS-managed IAM policy
// ARN. AWS-managed policies are not customer-owned — their lifecycle
// belongs to AWS — so importing one into customer Terraform state would
// surface permanent, unfixable drift (#652). The aws_iam_policy
// discoverer drops them via the SkipIdentifier hook.
func isAWSManagedPolicyARN(arn string) bool {
	for _, p := range awsManagedPolicyARNPrefixes {
		if strings.HasPrefix(arn, p) {
			return true
		}
	}
	return false
}

// vpcIDNativeIDs is a NativeIDsFromProperties extractor that lifts the
// CloudFormation model's VpcId into NativeIDs["vpc_id"] — the foreign
// key the #650 parent-instance resolver joins against an aws_vpc's
// ImportID. Returns nil when VpcId is absent so callers see "no native
// IDs" rather than an empty map.
func vpcIDNativeIDs(_ string, props map[string]any) map[string]string {
	if vpc := extractString(props, "VpcId"); vpc != "" {
		return map[string]string{"vpc_id": vpc}
	}
	return nil
}

// arnAndKey returns a NativeIDsFromProperties extractor that stamps the
// arnKey property under "arn" and, when present, the extraKey property
// under extraNativeKey. It is used by parent resources that also need to
// carry a reverse foreign key for the #650 parent-instance resolver —
// e.g. an aws_db_instance carrying its DBParameterGroupName so an
// aws_db_parameter_group child can be linked back to the instance that
// references it. Returns nil when neither property is present.
func arnAndKey(arnKey, extraKey, extraNativeKey string) func(string, map[string]any) map[string]string {
	return func(_ string, props map[string]any) map[string]string {
		out := map[string]string{}
		if arn := extractString(props, arnKey); arn != "" {
			out["arn"] = arn
		}
		if extra := extractString(props, extraKey); extra != "" {
			out[extraNativeKey] = extra
		}
		if len(out) == 0 {
			return nil
		}
		return out
	}
}

// fkRef pairs a CloudFormation property name with the NativeIDs key it
// is lifted under at discover time. The NativeIDs key MUST be a
// dependencies.FieldRefs() cross-ref field (role_arn, kms_key_arn,
// kms_key_id, kms_master_key_id, vpc_id, subnet_id, …) so the enrich-free
// closure resolver (composer/imported.DependencyEdges →
// dependencies.ResolveFromIdentities) can recover the same free-form FK
// edges reliable's Attrs-based picker closure produces today
// (presets#733). The CloudFormation property carrying the FK value is
// usually a different casing than the Terraform attribute (e.g. CFN
// `Role` → TF `role` → FieldRefs `role_arn`); fkRef makes the mapping
// explicit and testable.
type fkRef struct {
	// cfnProp is the CloudFormation GetResource property name carrying
	// the foreign-key value (an ARN / id / name pointing at another
	// resource), e.g. "Role", "KmsKeyArn", "KmsMasterKeyId".
	cfnProp string
	// nativeKey is the NativeIDs key to stamp the value under. It must
	// be a dependencies.FieldRefs() key so the closure resolver matches
	// it; TestFKLift_PerType pins this invariant for every wired type.
	nativeKey string
}

// fkNativeIDs returns a NativeIDsFromProperties extractor that lifts
// the cross-reference foreign-key fields in refs into NativeIDs at
// discover time, in addition to the resource's own canonical
// identifiers built by base. base may be nil for source types that have
// no own native identifiers worth carrying beyond the FK fields.
//
// Lifting these FK fields is the core presets#733 change: it makes the
// picker's free-form dependency closure (role → iam_role, kms_key_arn →
// kms_key, …) derivable from Identity alone, with no per-resource
// EnrichAttributes describe call. The FK value comes straight from the
// Cloud Control GetResource payload the discoverer already fetches.
//
// AWS allows a KMS FK to be a key id, key ARN, or alias — the closure
// resolver indexes the parent aws_kms_key by both its ImportID (key id)
// and NativeIDs["arn"], so a key-id or key-ARN reference joins; an
// alias-only reference resolves only when that alias is itself a
// discovered aws_kms_alias of the same target type (it is not, so an
// alias FK is conservatively dropped — matching reliable's Attrs path,
// which also only matches against arn/id, never alias).
func fkNativeIDs(base func(string, map[string]any) map[string]string, refs ...fkRef) func(string, map[string]any) map[string]string {
	return func(identifier string, props map[string]any) map[string]string {
		var out map[string]string
		if base != nil {
			out = base(identifier, props)
		}
		for _, r := range refs {
			val := extractString(props, r.cfnProp)
			if val == "" {
				continue
			}
			if out == nil {
				out = map[string]string{}
			}
			// First writer wins: never clobber a canonical identifier a
			// base extractor already set under the same key.
			if _, exists := out[r.nativeKey]; !exists {
				out[r.nativeKey] = val
			}
		}
		return out
	}
}

// fkNativeIDsNested is fkNativeIDs for a single FK value that lives one
// level down in the Cloud Control payload (e.g.
// AWS::DynamoDB::Table.SSESpecification.KMSMasterKeyId). objKey is the
// parent object property; cfnProp/nativeKey are as in fkRef.
func fkNativeIDsNested(base func(string, map[string]any) map[string]string, objKey, cfnProp, nativeKey string) func(string, map[string]any) map[string]string {
	return func(identifier string, props map[string]any) map[string]string {
		var out map[string]string
		if base != nil {
			out = base(identifier, props)
		}
		if obj, ok := props[objKey].(map[string]any); ok {
			if val := extractString(obj, cfnProp); val != "" {
				if out == nil {
					out = map[string]string{}
				}
				if _, exists := out[nativeKey]; !exists {
					out[nativeKey] = val
				}
			}
		}
		return out
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

// passthroughDomainNameNativeIDs builds the NativeIDs map for
// AWS::ApiGatewayV2::DomainName (#14j). The CC identifier IS the
// DomainName (the customer-visible domain string, also the primary
// identifier per the CFN schema); RegionalDomainName +
// DistributionDomainName are CloudFront / regional delivery-channel
// fronts that downstream tooling sometimes needs to resolve back to
// the parent domain. Stamp the canonical `domain_name` + the two
// alternate handles when present.
func passthroughDomainNameNativeIDs(identifier string, props map[string]any) map[string]string {
	out := map[string]string{"domain_name": identifier}
	if rd := extractString(props, "RegionalDomainName"); rd != "" {
		out["regional_domain_name"] = rd
	}
	if dd := extractString(props, "DistributionDomainName"); dd != "" {
		out["distribution_domain_name"] = dd
	}
	return out
}

// snsSubscriptionNameHint is the NameHintFromProperties for
// AWS::SNS::Subscription (#14j). The CFN schema has no top-level Name
// field — the most human-readable hint is Endpoint (e.g. an email
// address or SQS ARN), falling back to Protocol ("email", "sqs",
// "https", ...) and finally the SubscriptionArn identifier. The
// fall-through order matches the apigatewayv2_integration precedent
// (Description -> IntegrationType -> identifier).
func snsSubscriptionNameHint(identifier string, props map[string]any) string {
	if ep := extractString(props, "Endpoint"); ep != "" {
		return ep
	}
	if p := extractString(props, "Protocol"); p != "" {
		return p
	}
	return identifier
}

// snsSubscriptionNativeIDs builds the NativeIDs map for
// AWS::SNS::Subscription (#14j). The identifier IS the SubscriptionArn;
// the CC GetResource properties payload also surfaces TopicArn (the
// parent topic ARN), Endpoint, and Protocol. Stamp `arn` (canonical
// SubscriptionArn) + the three handles when present so downstream
// consumers can resolve a subscription by any of its observable
// identifiers — mirrors the IAM ServiceLinkedRole multi-handle native-
// IDs precedent from 14i.
func snsSubscriptionNativeIDs(identifier string, props map[string]any) map[string]string {
	out := map[string]string{"arn": identifier}
	if t := extractString(props, "TopicArn"); t != "" {
		out["topic_arn"] = t
	}
	if e := extractString(props, "Endpoint"); e != "" {
		out["endpoint"] = e
	}
	if p := extractString(props, "Protocol"); p != "" {
		out["protocol"] = p
	}
	return out
}

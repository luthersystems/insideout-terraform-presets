package awsdiscover

// cloudControlTypeConfigs is the registry of Terraform resource types
// routed through the generic Cloud Control discoverer. Each entry maps
// one TFType to a Cloud-Formation TypeName plus per-type extractors for
// import-ID, name-hint, native-ID, and tag-shape. The list is iterated
// at aggregator construction time (NewAWSDiscovererWithConcurrency in
// awsdiscover.go) to populate byType + serviceSlugByTFType in one shot.
//
// Adding a new type means: (1) confirm Cloud Control supports
// ListResources for the AWS::Service::Resource TypeName, (2) confirm
// the Cloud Control primary identifier matches the Terraform import
// format (or write an ImportIDFromIdentifier rewriter), (3) confirm
// the GetResource properties payload carries tags in a recognizable
// shape (flat map vs list-of-Key-Value), (4) append the config below,
// (5) extend pkg/insideout-import/registry/registry.go::awsTypes,
// (6) extend pkg/composer/imported/category.go::categoryByTFType,
// (7) extend pkg/insideout-import/permissions/aws.json with the
//     cloudcontrol:* and per-service Read permissions.
//
// All seven steps are policed by the existing parity tests
// (TestRegistryParity_AWS, TestCategory_TotalOverDiscoverRegistry,
// TestPermissionsManifest_CoversEveryService).
//
// Bundle 13 ships the framework + the aws_backup_vault prototype only.
// Bundle 13 Phase 4 (post-smoke-test, same PR) extends this list with
// the remaining Cloud Control-covered Tier A types. Cloud Control-
// uncovered types (e.g. aws_backup_selection, aws_cognito_user_pool_domain)
// defer to Bundle 14 hand-rolled discoverers.
var cloudControlTypeConfigs = []cloudControlConfig{
	{
		TFType:             "aws_backup_vault",
		CloudFormationType: "AWS::Backup::BackupVault",
		Slug:               "backup_vault",
		// Backup vaults are regional; not global.
		// Cloud Control returns the vault name as the primary
		// identifier, which is also the Terraform import ID for
		// aws_backup_vault — passthrough.
		ImportIDFromIdentifier: func(identifier string, _ map[string]any) string {
			return identifier
		},
		// Properties payload carries BackupVaultName as the canonical
		// name. Fallback to the identifier if absent — keeps the
		// emitted Address human-readable even when the payload shape
		// shifts.
		NameHintFromProperties: func(identifier string, props map[string]any) string {
			if name := extractString(props, "BackupVaultName"); name != "" {
				return name
			}
			return identifier
		},
		// AWS::Backup::BackupVault publishes a BackupVaultArn in the
		// properties payload — stamp it under NativeIDs for downstream
		// dep-chase to find the resource by ARN if necessary.
		NativeIDsFromProperties: func(_ string, props map[string]any) map[string]string {
			arn := extractString(props, "BackupVaultArn")
			if arn == "" {
				return nil
			}
			return map[string]string{"arn": arn}
		},
		// Tags shape: AWS::Backup::BackupVault.BackupVaultTags is a
		// flat map[string]string in CloudFormation schema (not the
		// list-of-Key-Value shape used by older services like
		// CloudWatch). Verified against:
		// https://docs.aws.amazon.com/AWSCloudFormation/latest/TemplateReference/aws-resource-backup-backupvault.html
		TagsFromProperties: func(props map[string]any) map[string]string {
			return extractStringMap(props, "BackupVaultTags")
		},
	},
}

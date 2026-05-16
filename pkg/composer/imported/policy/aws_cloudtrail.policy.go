package policy

// awsCloudtrailPolicy curates Layer 2 for `aws_cloudtrail`. Cloud-
// control-routed enrichment already produces typed Attrs; this map adds
// the curated surface to flip the type from Enrichable to
// DriftDetectable.
//
// CloudTrail logs AWS account activity to S3 (and optionally CloudWatch
// Logs / SNS). Identity is (name, arn, home_region). The trail's
// security posture is gated by:
//
//   - enable_log_file_validation  — integrity signing of the log files
//   - enable_logging              — master kill switch for the trail
//   - is_multi_region_trail       — whether the trail covers all regions
//   - is_organization_trail       — whether the trail covers all accounts
//   - kms_key_id                  — at-rest encryption of log payloads
//
// Each of those is a security-relevant flag — flipping or missing them
// is the canonical compliance regression we want to drift-detect.
//
// Drift bundle 4 (#482): scalar attributes use DriftSemanticExact;
// event_selector / advanced_event_selector / insight_selector blocks
// store nested lists of selector criteria — compared WholeList to
// reduce per-element noise from order-changes. Tags use tagPolicy().
//
// NOTE: bundle-4 promoted aws_cloudtrail from the no-policy fixture set.
// The test fixtures in pkg/composer/imported_*_test.go and
// pkg/imported/aws/provider_test.go that previously used aws_cloudtrail
// as their uncurated stand-in have been pivoted to aws_glacier_vault.
var awsCloudtrailPolicy = Map{
	// Identity ----------------------------------------------------------
	"arn": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"name": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"home_region": {
		// Region that owns the trail's metadata. Computed; never edited.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — log destinations ---------------------------------------
	"s3_bucket_name": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"s3_key_prefix": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"cloud_watch_logs_group_arn": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"cloud_watch_logs_role_arn": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"sns_topic_name": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"kms_key_id": {
		// KMS CMK encrypting the trail log payloads.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — security-relevant flags --------------------------------
	"enable_log_file_validation": {
		// Integrity signing of the log files — flipping to false is a
		// security regression. RequiresApproval.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"enable_logging": {
		// Master kill switch. Disabling the trail is a compliance event.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"include_global_service_events": {
		// Whether IAM/CloudFront global events are recorded.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"is_multi_region_trail": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"is_organization_trail": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Event selectors -- nested lists of selector criteria. Whole-list
	// compare so an unexpected add/remove surfaces as one diff entry.
	"event_selector": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticWholeList,
	},
	"advanced_event_selector": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticWholeList,
	},
	"insight_selector": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticWholeList,
	},

	// Tags --------------------------------------------------------------
	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
}

func init() {
	Register("aws_cloudtrail", awsCloudtrailPolicy)
}

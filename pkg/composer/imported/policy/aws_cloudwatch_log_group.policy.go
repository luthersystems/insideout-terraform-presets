package policy

// awsCloudwatchLogGroupPolicy curates Layer 2 for `aws_cloudwatch_log_group`.
//
// Bundle D2 (#491): DriftSemantic axis is curated on every non-tag entry.
// All curated leaves are scalar (ARN, name, KMS key ARN, retention days,
// log group class, skip_destroy bool) — DriftSemanticExact is the
// meaningful comparison. There are no list-valued or map-valued curated
// fields here, so WholeList / LabelFilter do not apply.
//
// #568: `tags` / `tags_all` adopt awsTagDriftPolicy() so user-set tags
// (notably the canonical `Project` tag that the InsideOut inspector
// uses to attribute resources — CLAUDE.md "Project tag is required on
// every taggable AWS resource") surface as `tags.<key>` per-key drift
// when stripped out-of-band. Log groups are a stable resource with
// low tag-churn, so noise-vs-signal trades favor surfacing user-set
// tag drift here.
var awsCloudwatchLogGroupPolicy = Map{
	// Identity
	"arn": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"name": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring
	"kms_key_id": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning
	"retention_in_days": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"log_group_class": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		ChangeRisk:    ChangeMayReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"skip_destroy": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Tags — adopt awsTagDriftPolicy() so user-set tag drift (esp.
	// the Project tag used by the InsideOut inspector) surfaces as
	// per-key `tags.<key>` mismatches with AWS-managed prefixes
	// (`aws:`, `eks:`, etc.) filtered out (#568).
	"tags":     awsTagDriftPolicy(),
	"tags_all": awsTagDriftPolicy(),
}

func init() {
	Register("aws_cloudwatch_log_group", awsCloudwatchLogGroupPolicy)
}

package policy

// awsCloudwatchLogGroupPolicy curates Layer 2 for `aws_cloudwatch_log_group`.
//
// Bundle D2 (#491): DriftSemantic axis is curated on every non-tag entry.
// All curated leaves are scalar (ARN, name, KMS key ARN, retention days,
// log group class, skip_destroy bool) — DriftSemanticExact is the
// meaningful comparison. There are no list-valued or map-valued curated
// fields here, so WholeList / LabelFilter do not apply. Tag bags stay
// DriftSemanticNone (tagPolicy() zero value) — provider noise on tags is
// filtered at a higher layer.
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

	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
}

func init() {
	Register("aws_cloudwatch_log_group", awsCloudwatchLogGroupPolicy)
}

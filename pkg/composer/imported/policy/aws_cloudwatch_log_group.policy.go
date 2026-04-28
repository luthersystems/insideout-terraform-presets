package policy

var awsCloudwatchLogGroupPolicy = Map{
	// Identity
	"arn":  {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever},
	"name": {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever},

	// Wiring
	"kms_key_id": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit: EditRelationshipOnly,
	},

	// Tuning
	"retention_in_days": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"log_group_class": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
		ChangeRisk: ChangeMayReplace,
	},
	"skip_destroy": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit: EditRequiresApproval,
	},

	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
}

func init() {
	Register("aws_cloudwatch_log_group", awsCloudwatchLogGroupPolicy)
}

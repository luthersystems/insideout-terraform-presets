package policy

var googleComputeHealthCheckPolicy = Map{
	// Identity
	"name":      {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever},
	"id":        {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever},
	"self_link": {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever},
	"project": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},
	"type": {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever},

	// Tuning — probe intervals, thresholds.
	"description": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"check_interval_sec": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit: EditChatSafe,
	},
	"timeout_sec": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit: EditChatSafe,
	},
	"healthy_threshold": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit: EditChatSafe,
	},
	"unhealthy_threshold": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit: EditChatSafe,
	},
	"source_regions": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit: EditRequiresApproval,
	},
	"creation_timestamp": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditNever,
	},

	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("google_compute_health_check", googleComputeHealthCheckPolicy)
}

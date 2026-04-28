package policy

var awsDynamodbTablePolicy = Map{
	// Identity
	"arn":        {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever},
	"id":         {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever},
	"name":       {Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever},
	"stream_arn": {Role: RoleIdentity, Visibility: VisibilityRileyVisible, Edit: EditNever},

	// Wiring (encryption, replication targets, restore source)
	"server_side_encryption.kms_key_arn": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit: EditRelationshipOnly,
	},
	"server_side_encryption.enabled": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit: EditRequiresApproval,
	},
	"replica.region_name": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit: EditRelationshipOnly,
	},
	"replica.kms_key_arn": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit: EditRelationshipOnly,
	},
	"restore_source_table_arn": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit: EditRelationshipOnly,
	},

	// Tuning — capacity and storage
	"billing_mode": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"read_capacity": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"write_capacity": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"table_class": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},

	// Tuning — backups and protection
	"point_in_time_recovery.enabled": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"deletion_protection_enabled": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit: EditRequiresApproval,
	},

	// TTL
	"ttl.enabled": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"ttl.attribute_name": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},

	// Streams
	"stream_enabled": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},
	"stream_view_type": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible, Edit: EditChatSafe,
	},

	// Keys (relationship-shaped — pinned, not chat-edited)
	"hash_key": {
		Role: RoleWiring, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},
	"range_key": {
		Role: RoleWiring, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},

	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),

	// timeouts singleton — system-owned operational metadata.
	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("aws_dynamodb_table", awsDynamodbTablePolicy)
}

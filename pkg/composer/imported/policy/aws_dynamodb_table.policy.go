package policy

// awsDynamodbTablePolicy is the hand-curated Layer 2 field-policy map for
// `aws_dynamodb_table`. It mirrors the source-of-truth TS projection in
// reliable's `lib/stack/imported/aws_dynamodb_table.policy.ts`; that
// file's `tests/imported-policy-projection.test.ts` locks the two
// against silent drift.
//
// Coverage decision (slice #1346 / presets bundle #461 follow-up): only
// the configurable surface that Riley can sensibly reason about is
// curated. Computed-only fields (arn, id, stream_arn, stream_label) are
// surfaced as Identity / Never / UIVisible so the diff screen can render
// them as read-only context without making them Riley-editable.
//
// Identity vs Wiring on top-level: `name` is the table's primary key
// and is `AlwaysReplace`; `hash_key` and `range_key` are the partition
// / sort keys that define the table's primary-key schema and share the
// same recreate semantics. The composer's drift comparator depends on
// the AlwaysReplace signal to surface the recreate-vs-update warning.
//
// Sibling references:
//   - aws_s3_bucket.policy.go — top-of-bundle template (#461)
//   - google_storage_bucket.policy.go — older GCP reference
//   - aws_dynamodb_table.gen.go — Layer 1 typed struct (every leaf
//     enumerated here has a corresponding tf-tagged field there)
var awsDynamodbTablePolicy = Map{
	// Identity ----------------------------------------------------------
	"arn": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
	},
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
	},
	"name": {
		// Table name is the primary key — changing it recreates the
		// resource. The drift comparator depends on AlwaysReplace.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},
	"hash_key": {
		// Partition key — part of the primary-key schema, recreate.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},
	"range_key": {
		// Sort key — same recreate semantics as hash_key.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},
	"stream_arn": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
	},
	"stream_label": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
	},

	// Tuning — capacity and storage ------------------------------------
	"billing_mode": {
		// Provisioned vs on-demand — reversible in-place. Riley should
		// be able to propose the flip for cost optimization.
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible,
		Edit: EditChatSafe,
	},
	"read_capacity": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible,
		Edit: EditChatSafe,
	},
	"write_capacity": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible,
		Edit: EditChatSafe,
	},
	"table_class": {
		// STANDARD vs STANDARD_INFREQUENT_ACCESS — cost optimization knob.
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible,
		Edit: EditChatSafe,
	},

	// Tuning — backups and protection ----------------------------------
	"point_in_time_recovery.enabled": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit: EditChatSafe,
	},
	"deletion_protection_enabled": {
		// Safety flag — Riley can propose flipping it, UI shows the change.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit: EditChatSafe,
	},

	// TTL --------------------------------------------------------------
	"ttl.enabled": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit: EditChatSafe,
	},
	"ttl.attribute_name": {
		// The TTL attribute name references a schema column — wiring,
		// not a free-form tuning knob.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit: EditRelationshipOnly,
	},

	// Streams ----------------------------------------------------------
	"stream_enabled": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit: EditChatSafe,
	},
	"stream_view_type": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit: EditChatSafe,
	},

	// Server-side encryption -------------------------------------------
	"server_side_encryption.enabled": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit: EditChatSafe,
	},
	"server_side_encryption.kms_key_arn": {
		// KMS key is a cross-resource wiring relationship — Riley edits
		// the relationship, the composer's graph resolver owns the ARN.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit: EditRelationshipOnly, ChangeRisk: ChangeMayReplace,
	},

	// Attribute schema (primary-key + index column declarations) -------
	// Every `attribute` block declares a column referenced by hash_key,
	// range_key, GSI, or LSI. Schema changes force replacement.
	"attribute.name": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},
	"attribute.type": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk: ChangeAlwaysReplace,
	},

	// Local secondary indexes (LSIs) -----------------------------------
	// LSIs are created with the table and cannot be added or removed
	// after the fact — provider replaces the table.
	"local_secondary_index.name": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible,
		Edit: EditRequiresApproval, ChangeRisk: ChangeAlwaysReplace,
	},
	"local_secondary_index.projection_type": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible,
		Edit: EditRequiresApproval, ChangeRisk: ChangeMayReplace,
	},
	"local_secondary_index.range_key": {
		// LSI sort key references an `attribute.name` — wiring.
		Role: RoleWiring, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible,
		Edit: EditRelationshipOnly, ChangeRisk: ChangeAlwaysReplace,
	},
	"local_secondary_index.non_key_attributes": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible,
		Edit: EditRequiresApproval, ChangeRisk: ChangeMayReplace,
	},

	// Global secondary indexes (GSIs) ----------------------------------
	// GSIs can be added/removed online but capacity and projection
	// changes are heavier — gate behind RequiresApproval.
	"global_secondary_index.name": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible,
		Edit: EditRequiresApproval, ChangeRisk: ChangeMayReplace,
	},
	"global_secondary_index.hash_key": {
		Role: RoleWiring, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible,
		Edit: EditRelationshipOnly, ChangeRisk: ChangeMayReplace,
	},
	"global_secondary_index.range_key": {
		Role: RoleWiring, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible,
		Edit: EditRelationshipOnly, ChangeRisk: ChangeMayReplace,
	},
	"global_secondary_index.projection_type": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible,
		Edit: EditRequiresApproval, ChangeRisk: ChangeMayReplace,
	},
	"global_secondary_index.non_key_attributes": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible,
		Edit: EditRequiresApproval, ChangeRisk: ChangeMayReplace,
	},
	"global_secondary_index.read_capacity": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible,
		Edit: EditChatSafe,
	},
	"global_secondary_index.write_capacity": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible,
		Edit: EditChatSafe,
	},

	// Replicas (multi-region tables) -----------------------------------
	"replica.region_name": {
		// Region selection is a cross-resource wiring decision.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit: EditRelationshipOnly,
	},
	"replica.kms_key_arn": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit: EditRelationshipOnly,
	},
	"replica.point_in_time_recovery": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit: EditRequiresApproval,
	},
	"replica.propagate_tags": {
		Role: RoleTuning, Visibility: VisibilityRileyVisible, Edit: EditRequiresApproval,
	},
	"replica.arn": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
	},
	"replica.stream_arn": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
	},
	"replica.stream_label": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
	},

	// Restore (write-once inputs at create) ----------------------------
	"restore_source_table_arn": {
		// Source table ARN is a cross-resource wiring reference.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit: EditRelationshipOnly,
	},
	"restore_source_name": {
		Role: RoleTuning, Visibility: VisibilityUIVisible, Edit: EditNever,
	},
	"restore_date_time": {
		Role: RoleTuning, Visibility: VisibilityUIVisible, Edit: EditNever,
	},
	"restore_to_latest_time": {
		Role: RoleTuning, Visibility: VisibilityUIVisible, Edit: EditNever,
	},

	// Tags (system-managed bag) ----------------------------------------
	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),

	// timeouts singleton — system-owned operational metadata -----------
	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("aws_dynamodb_table", awsDynamodbTablePolicy)
}

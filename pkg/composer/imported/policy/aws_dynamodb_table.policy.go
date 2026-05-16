package policy

// awsDynamodbTablePolicy is the hand-curated Layer 2 field-policy map for
// `aws_dynamodb_table`. It mirrors the source-of-truth TS projection in
// reliable's `lib/stack/imported/aws_dynamodb_table.policy.ts`; that
// file's `tests/imported-policy-projection.test.ts` locks the two
// against silent drift.
//
// Coverage decision (slice #1346 / presets bundle #461 follow-up): only
// the configurable surface that the interactive agent can sensibly
// reason about is curated. Computed-only fields (arn, id, stream_arn,
// stream_label) are surfaced as Identity / Never / UIVisible so the
// diff screen can render them as read-only context without making
// them agent-editable.
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
//
// Bundle D1 (#491): DriftSemantic axis is curated across all non-tag,
// non-timeouts entries. Scalar attributes (ARNs, names, modes,
// capacities, booleans) use DriftSemanticExact. The schema-shaped nested
// blocks (`attribute`, `local_secondary_index`, `global_secondary_index`,
// `replica`) are addressed as dotted leaves, not as whole lists — the
// curator already enumerated the leaves and each one is a scalar; per-
// leaf Exact compare is the right granularity (whole-list compare would
// drop the per-field diff specificity).
var awsDynamodbTablePolicy = Map{
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
		// Table name is the primary key — changing it recreates the
		// resource. The drift comparator depends on AlwaysReplace.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"hash_key": {
		// Partition key — part of the primary-key schema, recreate.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"range_key": {
		// Sort key — same recreate semantics as hash_key.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"stream_arn": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"stream_label": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — capacity and storage ------------------------------------
	"billing_mode": {
		// Provisioned vs on-demand — reversible in-place. The interactive
		// agent should be able to propose the flip for cost optimization.
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"read_capacity": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"write_capacity": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"table_class": {
		// STANDARD vs STANDARD_INFREQUENT_ACCESS — cost optimization knob.
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — backups and protection ----------------------------------
	"point_in_time_recovery.enabled": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"deletion_protection_enabled": {
		// Safety flag — the interactive agent can propose flipping it, UI shows the change.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// TTL --------------------------------------------------------------
	"ttl.enabled": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"ttl.attribute_name": {
		// The TTL attribute name references a schema column — wiring,
		// not a free-form tuning knob.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Streams ----------------------------------------------------------
	"stream_enabled": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"stream_view_type": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Server-side encryption -------------------------------------------
	"server_side_encryption.enabled": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"server_side_encryption.kms_key_arn": {
		// KMS key is a cross-resource wiring relationship — the interactive
		// agent edits the relationship, the composer's graph resolver owns
		// the ARN. Exact equality: a different ARN is real security drift.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		ChangeRisk:    ChangeMayReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Attribute schema (primary-key + index column declarations) -------
	// Every `attribute` block declares a column referenced by hash_key,
	// range_key, GSI, or LSI. Schema changes force replacement.
	"attribute.name": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"attribute.type": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Local secondary indexes (LSIs) -----------------------------------
	// LSIs are created with the table and cannot be added or removed
	// after the fact — provider replaces the table.
	"local_secondary_index.name": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"local_secondary_index.projection_type": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		ChangeRisk:    ChangeMayReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"local_secondary_index.range_key": {
		// LSI sort key references an `attribute.name` — wiring.
		Role: RoleWiring, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"local_secondary_index.non_key_attributes": {
		// List-valued non_key_attributes — order matters for the
		// provider's diff and a per-element diff is not meaningfully
		// scoped to a specific column, so compare the whole list.
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		ChangeRisk:    ChangeMayReplace,
		DriftSemantic: DriftSemanticWholeList,
	},

	// Global secondary indexes (GSIs) ----------------------------------
	// GSIs can be added/removed online but capacity and projection
	// changes are heavier — gate behind RequiresApproval.
	"global_secondary_index.name": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		ChangeRisk:    ChangeMayReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"global_secondary_index.hash_key": {
		Role: RoleWiring, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		ChangeRisk:    ChangeMayReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"global_secondary_index.range_key": {
		Role: RoleWiring, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		ChangeRisk:    ChangeMayReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"global_secondary_index.projection_type": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		ChangeRisk:    ChangeMayReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"global_secondary_index.non_key_attributes": {
		// Same WholeList rationale as the LSI variant.
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		ChangeRisk:    ChangeMayReplace,
		DriftSemantic: DriftSemanticWholeList,
	},
	"global_secondary_index.read_capacity": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"global_secondary_index.write_capacity": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Replicas (multi-region tables) -----------------------------------
	"replica.region_name": {
		// Region selection is a cross-resource wiring decision.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"replica.kms_key_arn": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"replica.point_in_time_recovery": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"replica.propagate_tags": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"replica.arn": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"replica.stream_arn": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"replica.stream_label": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Restore (write-once inputs at create) ----------------------------
	"restore_source_table_arn": {
		// Source table ARN is a cross-resource wiring reference.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"restore_source_name": {
		Role: RoleTuning, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"restore_date_time": {
		Role: RoleTuning, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"restore_to_latest_time": {
		Role: RoleTuning, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
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

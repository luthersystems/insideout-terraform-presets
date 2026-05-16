package policy

// awsMskConfigurationPolicy curates Layer 2 for `aws_msk_configuration`.
// Cloud-control-routed enrichment already produces typed Attrs; this map
// adds the curated surface to flip the type from Enrichable to
// DriftDetectable.
//
// An MSK configuration is a versioned broker-properties revision (the
// `server.properties`-shaped tuning blob applied to one or more
// `aws_msk_cluster` instances). Identity is (id, name, arn,
// latest_revision). The wiring surface is the Kafka version set the
// configuration is compatible with; the operational knob is the
// `server_properties` text itself.
//
// Drift bundle 5 (#482): scalar attributes use DriftSemanticExact; the
// `kafka_versions` list is set-shaped — WholeList compare.
// `server_properties` is multi-line text; diff Exact at the string level.
var awsMskConfigurationPolicy = Map{
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
	"latest_revision": {
		// The current revision number — bumps on every server_properties
		// edit. Identity for the active revision.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — version compatibility + broker properties --------------
	"kafka_versions": {
		// Set of Kafka versions this configuration is compatible with.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticWholeList,
	},
	"server_properties": {
		// Multi-line broker tuning text. RequiresApproval — a stale
		// in-prod edit is the drift we want to catch.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — description -------------------------------------------
	"description": {
		Role: RoleTuning, Visibility: VisibilityUIVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
}

func init() {
	Register("aws_msk_configuration", awsMskConfigurationPolicy)
}

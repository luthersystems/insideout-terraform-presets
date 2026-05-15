package policy

// awsRoute53ZonePolicy curates Layer 2 for `aws_route53_zone`. Cloud-
// control enrichment already produces typed Attrs; this map adds the
// curated surface to flip the type to DriftDetectable.
//
// Zone identity: (name, zone_id). The name is the DNS apex and is
// AlwaysReplace. Public vs private zone is determined by the presence
// of a `vpc` block — VPC associations are wiring, not tuning.
//
// Drift bundle (#482): scalar attributes use DriftSemanticExact;
// `name_servers` (a computed list returned by Route53) compares
// WholeList. Tags stay DriftSemanticNone via tagPolicy().
var awsRoute53ZonePolicy = Map{
	// Identity ----------------------------------------------------------
	"arn": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"zone_id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"name": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"primary_name_server": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"name_servers": {
		// Computed list of NS records — informational. Whole-list
		// compare: an unexpected change in the assigned NS set is one
		// diff entry.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticWholeList,
	},

	// Wiring — delegation set + VPC associations -----------------------
	"delegation_set_id": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"vpc.vpc_id": {
		// Private-zone VPC association. Cross-resource wiring.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"vpc.vpc_region": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning ------------------------------------------------------------
	"comment": {
		Role: RoleTuning, Visibility: VisibilityUIVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"force_destroy": {
		// Destructive flag — system-owned to keep the interactive agent from
		// flipping it as a chat-level edit.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditSystemOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Tags --------------------------------------------------------------
	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
}

func init() {
	Register("aws_route53_zone", awsRoute53ZonePolicy)
}

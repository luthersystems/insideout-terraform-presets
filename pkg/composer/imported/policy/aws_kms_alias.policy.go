package policy

// awsKMSAliasPolicy curates Layer 2 for `aws_kms_alias`. Cloud-control-
// routed enrichment already produces typed Attrs; this map adds the
// curated surface to flip the type from Enrichable to DriftDetectable.
//
// A KMS alias is a friendly-name pointer onto a CMK (target_key_id).
// Identity is (name, arn). The drift axis is `target_key_id` — alias
// retargeting flips a workload onto a different CMK without changing
// any caller-visible ARN, so out-of-band drift here is a real
// confidentiality regression.
//
// Drift bundle 8 (#482): scalars use DriftSemanticExact. No tag surface
// — aliases don't carry user tags.
var awsKMSAliasPolicy = Map{
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
		// "alias/<friendly-name>". Pinned at create.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"name_prefix": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"target_key_arn": {
		// Computed companion to target_key_id.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — target CMK ---------------------------------------------
	"target_key_id": {
		// Key ID / key ARN this alias points at. Retargeting is the
		// security-critical drift axis.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
}

func init() {
	Register("aws_kms_alias", awsKMSAliasPolicy)
}

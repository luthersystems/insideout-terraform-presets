package policy

// googleKmsKeyRingPolicy curates Layer 2 for `google_kms_key_ring`.
// Identity scalars (name / id / project / location) are tagged
// DriftSemanticExact — KMS keyrings are singleton-ish (cannot be
// deleted once created), so any drift on these identity leaves is a
// re-parenting event that must be flagged. There are no list-valued
// curated fields on the keyring surface; WholeList does not apply.
var googleKmsKeyRingPolicy = Map{
	// Identity
	"name": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"id": {
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"project": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"location": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("google_kms_key_ring", googleKmsKeyRingPolicy)
}

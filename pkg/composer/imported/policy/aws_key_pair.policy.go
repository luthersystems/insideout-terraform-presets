package policy

// awsKeyPairPolicy curates Layer 2 for `aws_key_pair`. Cloud-control-
// routed enrichment already produces typed Attrs; this map adds the
// curated surface to flip the type from Enrichable to DriftDetectable.
//
// An EC2 key pair is a registered SSH public key used for EC2 instance
// bootstrap. Identity is (key_name, key_pair_id, arn, fingerprint).
// `public_key` is the load-bearing key material that authorizes SSH —
// drift means an out-of-band key swap (effectively a different
// credential). `fingerprint` is a derived/computed mirror of the key
// material and is the cheapest drift check.
//
// Drift bundle 11 (#482): scalars use DriftSemanticExact. Tags use
// tagPolicy().
var awsKeyPairPolicy = Map{
	// Identity ----------------------------------------------------------
	"arn": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"key_pair_id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"key_name": {
		// Key-pair name; pinned at create.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"fingerprint": {
		// Provider-derived MD5 of the public key. Drift here flags an
		// out-of-band key swap without needing to diff the full body.
		Role: RoleIdentity, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Key material — security-critical ---------------------------------
	"public_key": {
		// SSH public key body authorized for the key pair. Security
		// boundary — any change is a credential rotation.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"key_type": {
		// rsa / ed25519; provider-derived from public_key.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Tags --------------------------------------------------------------
	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
}

func init() {
	Register("aws_key_pair", awsKeyPairPolicy)
}

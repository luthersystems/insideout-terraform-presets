package policy

// awsSsmParameterPolicy curates Layer 2 for `aws_ssm_parameter`. Cloud-
// control-routed enrichment already produces typed Attrs; this map adds
// the curated surface to flip the type from Enrichable to
// DriftDetectable.
//
// An SSM Parameter Store entry is config + secret material referenced by
// deploys (CloudFormation / EKS / Lambda). Identity is (name, arn);
// version is a monotonically-incrementing computed integer. The
// security-critical knobs are `type` (SecureString triggers KMS encryption)
// and `value` (the secret payload itself — Sensitive). Out-of-band drift
// on value flags an out-of-band rotation.
//
// Drift bundle 8 (#482): scalars use DriftSemanticExact. The `value`
// field carries SensitivityRedacted so it stays hidden in diff display
// even though drift is still detected. Tags use tagPolicy().
var awsSsmParameterPolicy = Map{
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
		// "/foo/bar/baz" hierarchical name. Pinned at create.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"version": {
		// Monotonically incrementing — bumps on every value edit.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Type + tier -----------------------------------------------------
	"type": {
		// String | StringList | SecureString. SecureString triggers KMS
		// envelope encryption — flipping back to String is a regression.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"tier": {
		// Standard | Advanced | Intelligent-Tiering. Capacity / cost knob.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"data_type": {
		// text | aws:ec2:image | aws:ssm:integration.
		Role: RoleTuning, Visibility: VisibilityUIVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — KMS key encrypting SecureString -----------------------
	"key_id": {
		// KMS CMK ID/ARN encrypting SecureString payloads (or default
		// aws/ssm alias if unset).
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Operator-controlled metadata ------------------------------------
	"description": {
		Role: RoleTuning, Visibility: VisibilityUIVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"allowed_pattern": {
		Role: RoleTuning, Visibility: VisibilityUIVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"overwrite": {
		// Provider-side flag — whether `terraform apply` clobbers an
		// existing parameter with the same name.
		Role: RoleTuning, Visibility: VisibilityUIVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Secret payload --------------------------------------------------
	"value": {
		// SecureString value — Sensitive. Drift here flags out-of-band
		// rotation (or tampering). The diff layer redacts the actual
		// value at display time.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		Sensitivity:   SensitivityRedacted,
		DriftSemantic: DriftSemanticExact,
	},
	"insecure_value": {
		// Plaintext-acknowledged value for non-SecureString types.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Tags --------------------------------------------------------------
	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
}

func init() {
	Register("aws_ssm_parameter", awsSsmParameterPolicy)
}

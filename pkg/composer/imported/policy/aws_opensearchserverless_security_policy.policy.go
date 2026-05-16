package policy

// awsOpensearchserverlessSecurityPolicyPolicy curates Layer 2 for
// `aws_opensearchserverless_security_policy`.
//
// An OpenSearch Serverless security policy attaches network or
// encryption rules to one or more serverless collections. Identity is
// (name, type=encryption|network). `policy` is a JSON document binding
// collection name/pattern rules to the chosen security axis:
//
//   - type=encryption — pins the KMS CMK (or AWS_OWNED_KMS_KEY) used
//     at-rest for matching collections.
//   - type=network — controls VPC endpoint vs. public reachability of
//     matching collections' API + dashboard endpoints.
//
// Drift on `policy` silently re-scopes who can reach the collection at
// the network boundary, or which CMK protects its at-rest indices.
//
// Drift bundle 13 (#482): scalars use DriftSemanticExact. No tag
// surface — security policies are not tagged. `policy` is compared as
// opaque text; canonical-JSON normalization happens at a higher diff
// layer.
var awsOpensearchserverlessSecurityPolicyPolicy = Map{
	// Identity ----------------------------------------------------------
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"name": {
		// Security-policy name; pinned at create.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"type": {
		// "encryption" or "network". Pinned at create.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — description --------------------------------------------
	"description": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Security-critical payload ---------------------------------------
	"policy": {
		// JSON document — collection name/pattern rules bound to
		// network reachability (VPC endpoint vs. public) or at-rest
		// encryption key choice. Security boundary — drift silently
		// re-scopes the collection's network or KMS posture.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Versioning — service-emitted ------------------------------------
	"policy_version": {
		// Provider-emitted version token; flips on each policy update.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
}

func init() {
	Register("aws_opensearchserverless_security_policy", awsOpensearchserverlessSecurityPolicyPolicy)
}

package policy

// awsOpensearchserverlessAccessPolicyPolicy curates Layer 2 for
// `aws_opensearchserverless_access_policy`.
//
// An OpenSearch Serverless access policy is the data-access IAM document
// attached to a serverless collection. Identity is (name, type=data).
// `policy` is a JSON document binding principals (IAM ARNs, SAML group
// names) to collection / index permissions (CreateIndex, DescribeIndex,
// ReadDocument, WriteDocument, etc.). Drift on `policy` is the
// security-critical surface — out-of-band changes silently re-grant
// data-plane access on serverless indices.
//
// Drift bundle 13 (#482): scalars use DriftSemanticExact. No tag
// surface — OpenSearch Serverless access policies are not tagged.
// `policy` is compared as opaque text; canonical-JSON normalization
// happens at a higher diff layer.
var awsOpensearchserverlessAccessPolicyPolicy = Map{
	// Identity ----------------------------------------------------------
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"name": {
		// Access-policy name; pinned at create.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"type": {
		// Policy type — currently only "data" is valid for access policies.
		// Pinned at create.
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
		// JSON document granting principal → collection/index data-plane
		// permissions. Security boundary — drift silently re-grants
		// who can read/write serverless indices.
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
	Register("aws_opensearchserverless_access_policy", awsOpensearchserverlessAccessPolicyPolicy)
}

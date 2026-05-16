package policy

// awsOpensearchserverlessCollectionPolicy curates Layer 2 for
// `aws_opensearchserverless_collection`.
//
// An OpenSearch Serverless collection is the top-level container for
// serverless OpenSearch indices. Identity is (name, id, arn). `type`
// (SEARCH / TIMESERIES / VECTORSEARCH) pins the underlying engine
// behavior and capacity model; `standby_replicas` (ENABLED / DISABLED)
// flips the availability tier; `kms_key_arn` is the security boundary
// for at-rest encryption.
//
// Drift bundle 12 (#482): scalars use DriftSemanticExact. Tags use
// tagPolicy().
var awsOpensearchserverlessCollectionPolicy = Map{
	// Identity ----------------------------------------------------------
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"arn": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"name": {
		// Collection name; pinned at create.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — engine type + availability tier ------------------------
	"type": {
		// SEARCH / TIMESERIES / VECTORSEARCH. Pinned at create.
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"standby_replicas": {
		// ENABLED / DISABLED. Flips availability tier and per-OCU
		// billing line.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"description": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Security — at-rest encryption -----------------------------------
	"kms_key_arn": {
		// CMK used for index data at rest. Provider-derived.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Endpoints — service-emitted ------------------------------------
	"collection_endpoint": {
		// Provider-derived OpenSearch API endpoint.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"dashboard_endpoint": {
		// Provider-derived OpenSearch Dashboards endpoint.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Tags --------------------------------------------------------------
	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
}

func init() {
	Register("aws_opensearchserverless_collection", awsOpensearchserverlessCollectionPolicy)
}

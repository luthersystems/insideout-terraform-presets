package policy

// awsLbListenerPolicy curates Layer 2 for `aws_lb_listener`. Cloud-control
// enrichment already produces typed Attrs; this map gives the drift
// comparator a curated surface so the type flips from Enrichable to
// DriftDetectable.
//
// Listener identity is (load_balancer_arn, port, protocol). The
// `default_action` block is the routing decision — diffs there are
// real wiring drift; we compare it WholeList because the action list
// is order-sensitive (the provider applies actions in declared order).
//
// Drift bundle (#482): scalar attributes use DriftSemanticExact;
// `default_action` uses DriftSemanticWholeList. Tags stay
// DriftSemanticNone via tagPolicy().
var awsLbListenerPolicy = Map{
	// Identity ----------------------------------------------------------
	"arn": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — listener attaches to a load balancer --------------------
	"load_balancer_arn": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"certificate_arn": {
		// ACM cert ARN — cross-resource wiring; a different ARN is real
		// security drift.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — protocol + port -----------------------------------------
	"port": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"protocol": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"ssl_policy": {
		// TLS policy version — security-sensitive; allow chat with
		// approval gating at the comparator layer.
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"alpn_policy": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityRileyVisible,
		Edit:          EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Default action — the routing decision ----------------------------
	// Compared WholeList: actions are order-sensitive and a per-leaf diff
	// in this deeply-nested block doesn't carry useful signal for the
	// drift surface.
	"default_action": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticWholeList,
	},

	// Mutual auth ------------------------------------------------------
	"mutual_authentication.mode": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"mutual_authentication.trust_store_arn": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"mutual_authentication.ignore_client_certificate_expiry": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilityRileyVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Tags --------------------------------------------------------------
	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),

	// timeouts singleton ------------------------------------------------
	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("aws_lb_listener", awsLbListenerPolicy)
}

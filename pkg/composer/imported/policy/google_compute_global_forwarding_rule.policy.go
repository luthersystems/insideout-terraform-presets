package policy

// googleComputeGlobalForwardingRulePolicy curates Layer 2 for
// `google_compute_global_forwarding_rule`. All curated leaves are
// scalar (target/network/subnetwork self-links, IP addr/proto/version,
// port range, LB scheme), so DriftSemanticExact is the meaningful
// comparison across the surface.
var googleComputeGlobalForwardingRulePolicy = Map{
	// Identity
	"name": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"id": {
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"self_link": {
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"project": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — target proxy + VPC.
	"target": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"network": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit: EditRelationshipOnly, ChangeRisk: ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"subnetwork": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit: EditRelationshipOnly, ChangeRisk: ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning
	"description": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"ip_address": {
		Role: RoleTuning, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"ip_protocol": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"ip_version": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"port_range": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditRequiresApproval,
		ChangeRisk:    ChangeMayReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"load_balancing_scheme": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit: EditNever, ChangeRisk: ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Labels
	"labels":           tagPolicy(),
	"effective_labels": tagPolicy(),
	"terraform_labels": tagPolicy(),

	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("google_compute_global_forwarding_rule", googleComputeGlobalForwardingRulePolicy)
}

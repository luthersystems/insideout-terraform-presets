package policy

// googleComputeAddressPolicy curates Layer 2 for `google_compute_address`.
//
// Bundle D3 (#491): DriftSemantic axis is curated on every non-label,
// non-timeouts entry. All curated leaves are scalar (IP address string,
// purpose/tier/type enums, subnetwork/network self-links, ip_version,
// prefix_length, description) — DriftSemanticExact is the meaningful
// comparison. There are no list-valued curated fields, so WholeList
// does not apply. Label bags stay DriftSemanticNone (tagPolicy() zero
// value); user-author label LabelFilter coverage is the comparator's
// redacted-mode follow-up tracked alongside axes.go.
var googleComputeAddressPolicy = Map{
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
	"region": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — subnetwork the address lives in (regional internal addresses).
	"subnetwork": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit: EditRelationshipOnly, ChangeRisk: ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"network": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit: EditRelationshipOnly, ChangeRisk: ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — address kind, type, tier.
	"address": {
		Role: RoleTuning, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"address_type": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"purpose": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"network_tier": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible,
		Edit: EditRequiresApproval, ChangeRisk: ChangeMayReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"ip_version": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"prefix_length": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"description": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Labels — system-owned.
	"labels":           tagPolicy(),
	"effective_labels": tagPolicy(),
	"terraform_labels": tagPolicy(),

	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("google_compute_address", googleComputeAddressPolicy)
}

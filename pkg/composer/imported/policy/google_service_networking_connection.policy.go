package policy

// googleServiceNetworkingConnectionPolicy curates Layer 2 for
// `google_service_networking_connection`.
//
// Connections wire a consumer VPC to a service producer (e.g. Cloud SQL
// private IP, Memorystore peering). Identity = (network, service);
// `peering` is output-only (the producer-side VPC peering name) and
// reserved_peering_ranges is the IP-range allocation that backs the
// connection — Wiring axis because changing it forces a teardown of
// the cross-VPC peering.
var googleServiceNetworkingConnectionPolicy = Map{
	// Identity
	"id": {Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever},
	"network": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"service": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"peering": {
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — the IP-range allocation that backs the peering. Changing
	// it requires reconnecting the connection on both sides.
	"reserved_peering_ranges": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — destroy-time policy + create-fail recovery flag.
	"deletion_policy": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"update_on_creation_fail": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("google_service_networking_connection", googleServiceNetworkingConnectionPolicy)
}

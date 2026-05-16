package policy

// googleVPCAccessConnectorPolicy curates Layer 2 for
// `google_vpc_access_connector`.
//
// Serverless VPC Access connector — the egress bridge for Cloud Run /
// Cloud Functions to reach Compute Engine VPC. Identity = (project,
// region, name); `network` + `ip_cidr_range` + `subnet` define the
// network wiring; min/max instances and machine_type are user tuning
// knobs the model can reason about (capacity tradeoffs).
var googleVPCAccessConnectorPolicy = Map{
	// Identity
	"id": {Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever},
	"name": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
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
	"self_link": {Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever},
	"state":     {Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever},

	// Wiring — VPC network attachment. Either `network` (Compute mode)
	// or `subnet` (Direct VPC egress) is set, not both.
	"network": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"ip_cidr_range": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"subnet.name": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"subnet.project_id": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"connected_projects": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — capacity controls. Costs scale with min/max instances.
	"machine_type": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"max_instances": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"max_throughput": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"min_instances": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"min_throughput": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("google_vpc_access_connector", googleVPCAccessConnectorPolicy)
}

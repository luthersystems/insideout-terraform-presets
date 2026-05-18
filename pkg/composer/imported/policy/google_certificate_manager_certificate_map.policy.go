package policy

// googleCertificateManagerCertificateMapPolicy curates Layer 2 for
// `google_certificate_manager_certificate_map`. A certificate map is a
// thin grouping container that binds many `certificate_map_entry` rows
// to one or more GCLB target proxies. Identity is (project, name);
// `gclb_targets` is server-emitted (lists the proxies currently
// consuming the map).
//
// Bundle (#599): part of the DNS+cert mega-bundle. Codegen-only — see
// gcpCodegenOnlyTypes. Description is the only freely-mutable user-set
// field; labels participate in drift via gcpLabelDriftPolicy().
var googleCertificateManagerCertificateMapPolicy = Map{
	// Identity ----------------------------------------------------------
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
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
	"create_time": {
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"update_time": {
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning ------------------------------------------------------------
	"description": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},

	// Server-emitted GCLB targets (reverse-binding from cert map →
	// target proxies that mount it).
	"gclb_targets.target_https_proxy": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"gclb_targets.target_ssl_proxy": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"gclb_targets.ip_configs.ip_address": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"gclb_targets.ip_configs.ports": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditNever,
		DriftSemantic: DriftSemanticWholeList,
	},

	// Labels ------------------------------------------------------------
	"labels":           gcpLabelDriftPolicy(),
	"effective_labels": tagPolicy(),
	"terraform_labels": tagPolicy(),

	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("google_certificate_manager_certificate_map", googleCertificateManagerCertificateMapPolicy)
}

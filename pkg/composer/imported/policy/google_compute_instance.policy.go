package policy

// googleComputeInstancePolicy curates Layer 2 for `google_compute_instance`.
//
// Bundle G5 (#482): DriftSemantic axis is curated on every non-tag,
// non-timeouts entry. Scalars use DriftSemanticExact. The runtime
// fields where order is meaningful — `service_account.scopes`,
// `resource_policies` — use DriftSemanticWholeList. Labels stay at
// the tagPolicy() default (DriftSemanticNone).
//
// `metadata` is a map but is intentionally NOT marked as a label-style
// filter target: metadata is the user-authored set of VM env values
// (ssh-keys, startup scripts, etc.) and every key is semantically
// load-bearing; treat as exact map equality at the comparator layer.
// We don't currently emit a per-key drift semantic for it; the
// Sensitivity=Redacted axis already gates display.
var googleComputeInstancePolicy = Map{
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
	"zone": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"hostname": {
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — top-level machine knobs.
	"machine_type": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilityUIVisible,
		Edit: EditRequiresApproval, ChangeRisk: ChangeMayReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"min_cpu_platform": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"description": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"can_ip_forward": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"deletion_protection": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible, Edit: EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"desired_status": {
		Role: RoleTuning, Visibility: VisibilityUIVisible, Edit: EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"allow_stopping_for_update": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"enable_display": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"resource_policies": {
		Role: RoleWiring, Visibility: VisibilitySummaryVisible, Edit: EditRelationshipOnly,
		DriftSemantic: DriftSemanticWholeList,
	},
	// NB: `tags` on compute_instance is NOT labels — it's the GCE network
	// tags list that drives firewall source_tags / target_tags, so the
	// wizard / interactive agent need to see them. Intentionally uncurated until
	// lint.go's tagAttrSuffixes can exempt this case (separate follow-up);
	// curating with tagPolicy() would mark it SystemOnly+Hidden, which is
	// wrong, and any non-SystemOnly curation trips CodeTagFieldNotSystemOnly.

	// Boot disk.
	"boot_disk.source": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit: EditRelationshipOnly, ChangeRisk: ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"boot_disk.device_name": {
		Role: RoleIdentity, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"boot_disk.auto_delete": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"boot_disk.initialize_params.image": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit: EditRequiresApproval, ChangeRisk: ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"boot_disk.initialize_params.size": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible,
		Edit: EditRequiresApproval, ChangeRisk: ChangeMayReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"boot_disk.initialize_params.type": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible,
		Edit: EditRequiresApproval, ChangeRisk: ChangeMayReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"boot_disk.kms_key_self_link": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit: EditRelationshipOnly, ChangeRisk: ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Network interfaces.
	"network_interface.network": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit: EditRelationshipOnly, ChangeRisk: ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"network_interface.subnetwork": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit: EditRelationshipOnly, ChangeRisk: ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"network_interface.network_ip": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"network_interface.access_config.nat_ip": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditRequiresApproval,
		ChangeRisk:    ChangeMayReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"network_interface.access_config.network_tier": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible,
		Edit: EditRequiresApproval, ChangeRisk: ChangeMayReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"network_interface.nic_type": {
		Role: RoleTuning, Pillar: PillarPerformance, Visibility: VisibilitySummaryVisible,
		Edit: EditNever, ChangeRisk: ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Service account.
	"service_account.email": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"service_account.scopes": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticWholeList,
	},

	// Scheduling.
	"scheduling.preemptible": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit: EditRequiresApproval, ChangeRisk: ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"scheduling.automatic_restart": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"scheduling.on_host_maintenance": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"scheduling.provisioning_model": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit: EditRequiresApproval, ChangeRisk: ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Shielded VM.
	"shielded_instance_config.enable_secure_boot": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"shielded_instance_config.enable_vtpm": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"shielded_instance_config.enable_integrity_monitoring": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Metadata: kv map that legitimately carries SSH keys, env config, and
	// user data scripts. Visible to the interactive agent + UI (operators need to see it),
	// edits require approval (instance-impacting), values redacted (may
	// contain credentials). DriftSemantic deferred — see file comment.
	"metadata": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit: EditRequiresApproval, Sensitivity: SensitivityRedacted,
	},
	"metadata_startup_script": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit: EditRequiresApproval, Sensitivity: SensitivityRedacted,
		DriftSemantic: DriftSemanticExact,
	},

	// Labels
	"labels":           tagPolicy(),
	"effective_labels": tagPolicy(),
	"terraform_labels": tagPolicy(),

	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("google_compute_instance", googleComputeInstancePolicy)
}

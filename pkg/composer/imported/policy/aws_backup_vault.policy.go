package policy

// awsBackupVaultPolicy curates Layer 2 for `aws_backup_vault`. Cloud-
// control-routed enrichment already produces typed Attrs; this map adds
// the curated surface to flip the type from Enrichable to
// DriftDetectable.
//
// A Backup Vault is the destination for AWS Backup jobs. Identity is
// (name, arn). The KMS key encrypting recovery points is wiring;
// force_destroy is a destructive tuning knob.
//
// Drift bundle 4 (#482): scalar attributes use DriftSemanticExact. The
// type has no list-shaped fields beyond the singleton timeouts block.
// Tags use tagPolicy().
var awsBackupVaultPolicy = Map{
	// Identity ----------------------------------------------------------
	"arn": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"name": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"recovery_points": {
		// Computed count of recovery points stored in the vault.
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — KMS key encrypting the vault ---------------------------
	"kms_key_arn": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — destructive flag ---------------------------------------
	"force_destroy": {
		// Allows deleting a vault that still has recovery points. System-
		// owned to keep the interactive agent from flipping it as a chat-
		// level edit.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditSystemOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Tags --------------------------------------------------------------
	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),

	// timeouts singleton ----------------------------------------------
	"timeouts": timeoutsPolicy(),
}

func init() {
	Register("aws_backup_vault", awsBackupVaultPolicy)
}

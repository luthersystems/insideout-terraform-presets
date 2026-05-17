package policy

// awsGlueCatalogDatabasePolicy curates Layer 2 for `aws_glue_catalog_database`.
// Cloud-control-routed enrichment already produces typed Attrs; this map
// adds the curated surface to flip the type from Enrichable to
// DriftDetectable.
//
// A Glue Catalog Database is the namespace under which Glue tables /
// connections live. Identity is (name, catalog_id, arn). Federated /
// target database blocks wire the catalog to an external metastore.
//
// NOTE: substituted into Bundle 4 in place of aws_cognito_user_pool — the
// codegen for that type trips a struct/var name collision (the resource's
// `schema` nested block generates a Go type named AWSCognitoUserPoolSchema
// that clashes with the resource's generated `<Type>Schema` variable).
//
// Drift bundle 4 (#482): scalar attributes use DriftSemanticExact;
// create_table_default_permission is a list-shaped block compared
// WholeList. Tags + parameters use tagPolicy()-style treatment.
var awsGlueCatalogDatabasePolicy = Map{
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
	"catalog_id": {
		// Owning Glue Data Catalog (defaults to the account).
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — descriptive metadata -----------------------------------
	"description": {
		Role: RoleTuning, Visibility: VisibilityUIVisible, Edit: EditChatSafe,
		DriftSemantic: DriftSemanticExact,
	},
	"location_uri": {
		// Default storage location for tables in this database.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},
	"parameters": {
		// Free-form K/V properties — system-owned to avoid drift noise.
		Role: RoleTuning, Visibility: VisibilityHidden, Edit: EditSystemOnly,
		Sensitivity:   SensitivityRedacted,
		DriftSemantic: DriftSemanticNone,
	},

	// Default permissions for new tables (list-shaped block) ----------
	"create_table_default_permission": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticWholeList,
	},
	"create_table_default_permission.permissions": {
		Role: RoleTuning, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticWholeList,
	},
	"create_table_default_permission.principal.data_lake_principal_identifier": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Federated / target database wiring (external metastore) ---------
	"federated_database.connection_name": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"federated_database.identifier": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"target_database.catalog_id": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"target_database.database_name": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"target_database.region": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Tags --------------------------------------------------------------
	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
}

func init() {
	Register("aws_glue_catalog_database", awsGlueCatalogDatabasePolicy)
}

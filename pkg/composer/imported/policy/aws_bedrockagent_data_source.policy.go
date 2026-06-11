package policy

// awsBedrockagentDataSourcePolicy curates Layer 2 for
// `aws_bedrockagent_data_source`.
//
// #787 backfill for the Bedrock Agents stack (#762 / #776). A data
// source is the ingestion side of a knowledge base: it binds a corpus
// (an S3 bucket or a SaaS connector) to a parent knowledge_base_id and
// configures how documents are chunked / parsed before embedding. The
// high-value drift surfaces are:
//
//   - knowledge_base_id — the parent KB this source feeds
//     (replace-on-change wiring).
//   - data_deletion_policy — RETAIN | DELETE; a silent flip to DELETE
//     means removing the source also purges the embedded vectors.
//   - data_source_configuration.type + the S3 bucket_arn — which corpus
//     gets ingested; rebinding ingests a different document set.
//   - server_side_encryption_configuration.kms_key_arn — at-rest
//     protection of the ingested + transient content.
//
// The provider exposes many SaaS connector trees (Confluence,
// Salesforce, SharePoint, Web) and a deep vector_ingestion_configuration
// chunking/parsing tree; curation targets the discriminator + the S3
// path this preset stack uses, leaving the connector-specific subtrees
// uncurated per the conservative codegen-only convention.
var awsBedrockagentDataSourcePolicy = Map{
	// Identity ----------------------------------------------------------
	"data_source_id": {
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
	"region": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — parent knowledge base ---------------------------------
	"knowledge_base_id": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Tuning — deletion semantics ------------------------------------
	"data_deletion_policy": {
		// RETAIN | DELETE — a silent flip to DELETE purges embedded
		// vectors when the source is removed.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	// Corpus binding — which documents get ingested ------------------
	"data_source_configuration.type": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"data_source_configuration.s3_configuration.bucket_arn": {
		// Rebinding ingests a different document set.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"data_source_configuration.s3_configuration.inclusion_prefixes": {
		// Which S3 prefixes get ingested — same bucket, different corpus.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticWholeList,
	},

	// SaaS connector credentials — security-critical auth wiring ------
	// Each connector authenticates via a Secrets Manager ARN. A silent
	// rebind points ingestion at different credentials (and thus a
	// potentially different tenant / corpus). Curated as Security wiring,
	// Redacted so the ARN is shown for context but never treated as a
	// raw secret value.
	"data_source_configuration.confluence_configuration.source_configuration.credentials_secret_arn": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		Sensitivity:   SensitivityRedacted,
		DriftSemantic: DriftSemanticExact,
	},
	"data_source_configuration.salesforce_configuration.source_configuration.credentials_secret_arn": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		Sensitivity:   SensitivityRedacted,
		DriftSemantic: DriftSemanticExact,
	},
	"data_source_configuration.share_point_configuration.source_configuration.credentials_secret_arn": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		Sensitivity:   SensitivityRedacted,
		DriftSemantic: DriftSemanticExact,
	},

	// Vector ingestion — how documents are chunked before embedding ---
	// chunking_strategy is replace-on-change; a silent edit re-chunks the
	// retrieved excerpts and changes retrieval behavior against the corpus.
	"vector_ingestion_configuration.chunking_configuration.chunking_strategy": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Encryption — at-rest protection of ingested content ------------
	"server_side_encryption_configuration.kms_key_arn": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
}

func init() {
	Register("aws_bedrockagent_data_source", awsBedrockagentDataSourcePolicy)
}

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

package policy

// awsBedrockagentKnowledgeBasePolicy curates Layer 2 for
// `aws_bedrockagent_knowledge_base`.
//
// #787 backfill for the Bedrock Agents stack (#762 / #776). A knowledge
// base is the RAG retrieval backbone an agent queries. It binds the IAM
// role Bedrock assumes to read the embedding model + vector store, the
// embedding model itself, and the vector store backend
// (storage_configuration). The high-value drift surfaces are:
//
//   - role_arn — the identity Bedrock assumes to embed + retrieve. A
//     silent rebind re-purposes the knowledge base's IAM identity.
//   - knowledge_base_configuration.vector_knowledge_base_configuration.
//     embedding_model_arn — the model that produced the stored
//     embeddings. A silent swap corrupts retrieval against the existing
//     corpus.
//   - storage_configuration.type + the s3_vectors_configuration binding
//     (index_arn / vector_bucket_arn) — which vector store the
//     embeddings live in; rebinding points the knowledge base at a
//     different corpus.
//
// The provider exposes a wide union of storage backends (Pinecone, RDS,
// OpenSearch, Mongo, Redis, Neptune, S3 Vectors); curation targets the
// store discriminator + the S3 Vectors binding that this preset stack
// uses (the s3vectors_* resources in this same bundle), leaving the
// other backend-specific field-mapping trees uncurated per the
// conservative codegen-only convention.
var awsBedrockagentKnowledgeBasePolicy = Map{
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
	"region": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — retrieval IAM identity --------------------------------
	"role_arn": {
		// Silent rebind re-purposes the knowledge base's IAM identity.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Embedding model — must match the stored corpus -----------------
	"knowledge_base_configuration.type": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"knowledge_base_configuration.vector_knowledge_base_configuration.embedding_model_arn": {
		// A silent swap corrupts retrieval against the existing corpus.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Storage backend — which corpus the base points at --------------
	"storage_configuration.type": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"storage_configuration.s3_vectors_configuration.index_arn": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"storage_configuration.s3_vectors_configuration.vector_bucket_arn": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
}

func init() {
	Register("aws_bedrockagent_knowledge_base", awsBedrockagentKnowledgeBasePolicy)
}

package policy

// awsS3vectorsIndexPolicy curates Layer 2 for `aws_s3vectors_index`.
//
// #787 backfill for the S3 Vectors backbone of the Bedrock Knowledge
// Base RAG stack (#783). A vector index pins the geometry of the
// embedding space: data_type, dimension, and distance_metric are all
// replace-on-change and must match the embedding model the knowledge
// base writes with — a silent mismatch corrupts retrieval. The index is
// wired to its parent store via vector_bucket_name. The encryption
// configuration governs at-rest protection of the embeddings.
var awsS3vectorsIndexPolicy = Map{
	// Identity ----------------------------------------------------------
	"index_arn": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"index_name": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"region": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — parent vector bucket ----------------------------------
	"vector_bucket_name": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Embedding-space geometry — must match the writing model --------
	// All three are replace-on-change; a silent edit corrupts retrieval
	// against an existing corpus.
	"data_type": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"dimension": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},
	"distance_metric": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Encryption — at-rest protection of embeddings ------------------
	"encryption_configuration": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Filterability of metadata keys (replace-on-change) -------------
	"metadata_configuration.non_filterable_metadata_keys": {
		Role: RoleTuning, Visibility: VisibilitySummaryVisible, Edit: EditRequiresApproval,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticWholeList,
	},

	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
}

func init() {
	Register("aws_s3vectors_index", awsS3vectorsIndexPolicy)
}

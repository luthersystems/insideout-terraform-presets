package policy

// awsKendraDataSourcePolicy curates Layer 2 for `aws_kendra_data_source`.
//
// #801 backfill for the aws/kendra preset (#760), deferred there because
// the PR had no terraform locally to refresh the pinned 6.45.0 provider
// schema, and matching the #787 / #795 AI-stack precedent. A data source
// is the connector that crawls a backend (the preset emits the S3 shape)
// into a parent Kendra index; Kendra assumes role_arn to read the backend
// and push documents into the index. The load-bearing drift surfaces are:
//
//   - index_id — the parent index this data source attaches to. Required
//     and replace-on-change; a silent rebind moves the connector to a
//     different index.
//   - role_arn — the IAM identity Kendra assumes to crawl the backend
//     (s3:GetObject/ListBucket on the wired bucket, BatchPutDocument into
//     the index). A silent rebind re-purposes the connector's IAM
//     identity. Security wiring.
//   - type — S3 / WEBCRAWLER / TEMPLATE. Immutable, so the provider
//     replaces the connector on change; a silent edit reshapes what the
//     data source ingests.
//   - configuration.s3_configuration.bucket_name — for the S3 shape this
//     preset emits, the bucket Kendra crawls. A silent rebind points the
//     index at a different corpus.
//   - custom_document_enrichment_configuration.role_arn + the pre/post
//     extraction-hook lambda_arn — an optional enrichment stage that runs
//     in the ingestion path. The role is an IAM identity (a silent rebind
//     re-purposes it) and each hook Lambda observes/mutates every crawled
//     document, so both are Security wiring (analogous to the #795
//     interceptor-lambda surface).
//   - schedule — the crawl cron. An in-place tuning knob, but a silent
//     change alters how fresh the index stays (and the crawl's cost), so
//     it is curated RequiresApproval with Exact drift rather than left to
//     the codegen default.
//
// The provider exposes a very deep configuration tree (web-crawler
// auth/proxy, template config, document-metadata mapping). Curation
// targets the parent binding, the crawl identity, the S3 backend this
// preset wires, and the enrichment-hook IAM/Lambda surfaces; the deeper
// per-shape config trees are left to the conservative codegen-only
// default. Unlike most resources this type IS taggable, so tags/tags_all
// are curated via tagPolicy().
var awsKendraDataSourcePolicy = Map{
	// Identity ----------------------------------------------------------
	"arn": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"id": {
		Role: RoleIdentity, Visibility: VisibilityUIVisible, Edit: EditNever,
		DriftSemantic: DriftSemanticExact,
	},
	"data_source_id": {
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

	// Wiring — parent index ------------------------------------------
	"index_id": {
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Wiring — crawl IAM identity ------------------------------------
	"role_arn": {
		// Silent rebind re-purposes the connector's IAM identity.
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Connector shape — replace-shaped enum --------------------------
	"type": {
		// S3 / WEBCRAWLER / TEMPLATE — immutable; provider replaces the
		// connector on change.
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRequiresApproval,
		ChangeRisk:    ChangeAlwaysReplace,
		DriftSemantic: DriftSemanticExact,
	},

	// Backend binding — what the S3 connector crawls -----------------
	"configuration.s3_configuration.bucket_name": {
		// A silent rebind points the index at a different corpus.
		Role: RoleWiring, Pillar: PillarReliability, Visibility: VisibilityUIVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Enrichment stage — IAM + Lambda in the ingestion path ----------
	// The enrichment role is the IAM identity Kendra assumes to run the
	// enrichment hooks; each pre/post-extraction hook Lambda observes and
	// can mutate every crawled document. A silent rebind of any re-points
	// the ingestion path at a different identity / different code.
	"custom_document_enrichment_configuration.role_arn": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"custom_document_enrichment_configuration.pre_extraction_hook_configuration.lambda_arn": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},
	"custom_document_enrichment_configuration.post_extraction_hook_configuration.lambda_arn": {
		Role: RoleWiring, Pillar: PillarSecurity, Visibility: VisibilitySummaryVisible,
		Edit:          EditRelationshipOnly,
		DriftSemantic: DriftSemanticExact,
	},

	// Crawl cadence — freshness / cost knob --------------------------
	"schedule": {
		Role: RoleTuning, Pillar: PillarReliability, Visibility: VisibilitySummaryVisible,
		Edit:          EditRequiresApproval,
		DriftSemantic: DriftSemanticExact,
	},

	"tags":     tagPolicy(),
	"tags_all": tagPolicy(),
}

func init() {
	Register("aws_kendra_data_source", awsKendraDataSourcePolicy)
}

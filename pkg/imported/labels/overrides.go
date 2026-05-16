// overrides.go centralises the curated display-label + icon-key
// overrides that diverge from the default humanise-snake-case rule in
// labels.go.
//
// Why this file exists: every entry below pins a string that has
// already shipped in luthersystems/reliable's
// components/import/serviceMeta.ts (the hand-maintained UI copy table
// that the upstream codegen will replace). Without these overrides the
// reliable Surface D consumer migration (reliable#1479 / reliable PR
// #1538) would regress user-visible product copy — "DynamoDB" would
// become "Dynamodb", "Pub/Sub" would become "Pubsub", "S3 bucket"
// would become "S3 Bucket", and so on.
//
// Authoring rules:
//   - Only override types that (a) are present in registry's
//     SupportedDiscoverTypes (AWS or GCP), and (b) need a label or icon
//     key that the default rule cannot produce.
//   - Match reliable's serviceMeta.ts label string verbatim. The
//     capitalisation, vendor brand spelling, and second-word case are
//     deliberate product copy choices — the labels_test.go cases lock
//     them in so a future driveby edit fails loudly.
//   - Icon keys are the lowercase semantic identifier matching
//     serviceMeta.ts's SVG basenames (so a downstream consumer can map
//     iconKey → /aws-icons/<iconKey>.svg / /gcp-icons/<iconKey>.svg
//     without a second lookup table). Where reliable's serviceMeta.ts
//     uses a tile icon different from its category (e.g. amber
//     ImportedTile genericIcon kinds), that's a tile-level concern that
//     stays in the reliable repo — this file only pins the canonical
//     per-type icon asset key.
//
// Future curation for types reliable doesn't have today can land in
// follow-up PRs as those types become user-visible.
package labels

func init() {
	registerCuratedOverrides()
}

// registerCuratedOverrides is the body of init(), factored out so the
// label-locking test can re-run it after a resetForTest() wipe. Adding
// a new override means: extend this function AND the test table in
// TestCuratedOverrides_LockReliableCopy. The two stay in lockstep by
// design — a missing test row is the loud signal a label change
// happened without consumer-side coordination.
//
// NOTE: reliable's serviceMeta.ts also carries entries for
// `aws_rds_cluster` and `google_compute_subnetwork`, which are NOT in
// registry.SupportedDiscoverTypes today. They are tracked as a
// follow-up (either add to upstream registry, or drop from reliable
// on the consumer-side swap). Registering them here would silently
// expand the emitted labels map beyond the discover-supported set
// and confuse downstream consumers that key off it.
func registerCuratedOverrides() {
	// AWS — importable today (matching serviceMeta.ts "importable today" section).
	Register("aws_sqs_queue", "Queue (SQS)", "sqs")
	Register("aws_dynamodb_table", "Table (DynamoDB)", "ddb")
	Register("aws_cloudwatch_log_group", "Log group (CloudWatch)", "cw")
	Register("aws_secretsmanager_secret", "Secret (Secrets Manager)", "secretsmanager")
	Register("aws_lambda_function", "Function (Lambda)", "lambda")

	// AWS — surfaced unsupported (matching serviceMeta.ts
	// "surfaced unsupported" section; reliable renders these as
	// disabled tiles in the import wizard).
	Register("aws_iam_role", "IAM role", "aws")
	Register("aws_iam_policy", "IAM policy", "aws")
	Register("aws_kms_key", "KMS key", "kms")
	Register("aws_s3_bucket", "Bucket (S3)", "s3")
	Register("aws_vpc", "Virtual private cloud (VPC)", "vpc")
	Register("aws_subnet", "Subnet", "vpc")
	Register("aws_security_group", "Security group", "vpc")
	Register("aws_eks_cluster", "Kubernetes cluster (EKS)", "eks")
	Register("aws_eks_node_group", "EKS node group", "eks")
	Register("aws_lb", "Load balancer (ALB)", "alb")
	Register("aws_cloudfront_distribution", "CDN (CloudFront)", "cdn")
	Register("aws_instance", "EC2 instance", "ec2")

	// GCP — importable today.
	Register("google_pubsub_topic", "Pub/Sub topic", "pubsub")
	Register("google_pubsub_subscription", "Pub/Sub subscription", "pubsub")
	Register("google_storage_bucket", "Cloud Storage bucket", "gcs")
	Register("google_secret_manager_secret", "Secret (Secret Manager)", "secret_manager")
	Register("google_compute_network", "VPC network", "vpc")

	// GCP — surfaced unsupported.
	Register("google_sql_database_instance", "Cloud SQL instance", "cloudsql")
	Register("google_container_cluster", "Kubernetes cluster (GKE)", "gke")
}

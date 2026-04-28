package main

// WantedAWS lists the Phase 1 AWS resource types we generate Layer 1
// structs for. Add new types here to expand coverage.
var WantedAWS = []string{
	"aws_sqs_queue",
	"aws_dynamodb_table",
	"aws_cloudwatch_log_group",
	"aws_secretsmanager_secret",
	"aws_lambda_function",
}

// WantedGoogle lists the Phase 1 GCP resource types.
var WantedGoogle = []string{
	"google_storage_bucket",
	"google_compute_network",
	"google_secret_manager_secret",
	"google_pubsub_topic",
	"google_pubsub_subscription",
}

// AWSProviderSource is the Terraform Registry source string for the AWS
// provider. Pinned in schemas/providers.tf and persisted via the generated
// version.gen.go.
const AWSProviderSource = "registry.terraform.io/hashicorp/aws"

// GoogleProviderSource is the Terraform Registry source string for the
// Google provider.
const GoogleProviderSource = "registry.terraform.io/hashicorp/google"

// SchemaCodegenVersion is bumped whenever the generator's output format
// changes in a way that breaks readers of existing generated files.
// Persisted into the generated version.gen.go.
const SchemaCodegenVersion = "1"

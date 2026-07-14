package imported

import "strings"

// TerraformStateBucketType is the AWS Terraform resource type used to back a
// Terraform S3 state backend. InsideOut/sandbox deploys provision one such
// bucket per project for their own Terraform state; a reverse-import
// already-managed guard recognizes it so a deploy's own state bucket is never
// offered as an importable customer resource.
//
// Exposed as presets-owned vocabulary so consumers do not hardcode the
// "aws_s3_bucket" type literal — per-type Terraform vocabulary lives upstream
// (reliable#2239, umbrella #1479). The deploy-specific bucket NAMING
// convention (e.g. luther-<project>-…-tfstate-s3-…) stays with the consumer
// that owns the deploy pipeline; only the resource-type gate is presets
// vocabulary.
const TerraformStateBucketType = "aws_s3_bucket"

// IsTerraformStateBucketType reports whether tfType is the AWS Terraform S3
// state-backend resource type (TerraformStateBucketType). Surrounding
// whitespace is trimmed to match callers that gate on a raw Identity.Type
// string.
func IsTerraformStateBucketType(tfType string) bool {
	return strings.TrimSpace(tfType) == TerraformStateBucketType
}

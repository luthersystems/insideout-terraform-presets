// overrides.go centralises the curated ForceNew overrides for fields
// where the provider's Go schema sets ForceNew=true but the JSON schema
// dump strips that information (see the package doc).
//
// Authoring rules:
//   - Every entry must be sourced from the upstream provider's Go
//     resource definition. Cite the provider repo + file + field in the
//     comment next to the call so reviewers can verify the override
//     against the canonical source.
//   - Mirror each Register() call with a row in
//     TestCuratedOverrides_PinForceNewExpectations (overrides_test.go).
//     The test fails loudly if an entry is added / changed without
//     coordinating the consumer-facing expectation.
//   - Only top-level attributes today. Nested-block fields keep the
//     ReplacementUnknown default; supporting nested paths is a
//     follow-up (registry key would extend to a "type.field.sub"
//     dotted form).
package forcenew

import (
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

func init() {
	registerCuratedOverrides()
}

// registerCuratedOverrides is the body of init(), factored out so the
// pin-test can re-run it after resetForTest() wipes the registry.
// Adding a new override means: extend this function AND the test table
// in TestCuratedOverrides_PinForceNewExpectations. The two stay in
// lockstep — a missing test row is the loud signal a behavior change
// happened without consumer-side coordination.
//
// Issue #566 seeded this with the aws_s3_bucket fields reliable's
// imported-registry.test.ts:73 asserts; expand opportunistically as
// follow-up PRs cover more resources.
func registerCuratedOverrides() {
	// aws_s3_bucket — per terraform-provider-aws
	// internal/service/s3/bucket.go ResourceBucket schema:
	//   "bucket":        { ForceNew: true, ... }
	//   "bucket_prefix": { ForceNew: true, ... }
	Register("aws_s3_bucket", "bucket", generated.ReplacementAlwaysReplace)
	Register("aws_s3_bucket", "bucket_prefix", generated.ReplacementAlwaysReplace)
}

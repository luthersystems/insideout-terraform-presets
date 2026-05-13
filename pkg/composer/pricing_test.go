package composer

// Tests for the PricingData / PricingItem / PricingBackups type family.
// Migrated from reliable per luthersystems/reliable#1437 PR-3.

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPricingData_Normalize_RoundTrip locks the cross-cloud scrub +
// legacy-field sync behaviour of PricingData.Normalize across two shapes:
//
//   - An AWS-shaped PricingData is idempotent under Normalize (calling it
//     twice yields the same result) — locks the contract that the AWS path
//     doesn't accumulate residue.
//   - A GCP-shaped PricingData scrubs AWS residue: a phantom aws_lambda row
//     persisted from a prior AWS turn MUST be cleared once the user has
//     switched to GCP.
//
// This mirrors the reliable-side behaviour the merge layer depends on
// (MergePricing calls fresh.Normalize() before the walk).
func TestPricingData_Normalize_RoundTrip(t *testing.T) {
	// AWS-shaped: Normalize is idempotent.
	awsData := pricingFromJSON(t, `{"_guidance_version":"v1","components":{
		"aws_lambda":{"monthlyUSD":1.87},
		"aws_s3":{"monthlyUSD":0.23}
	}}`)
	awsData.Normalize()
	once, err := json.Marshal(awsData)
	require.NoError(t, err)
	awsData.Normalize()
	twice, err := json.Marshal(awsData)
	require.NoError(t, err)
	assert.JSONEq(t, string(once), string(twice),
		"PricingData.Normalize must be idempotent on AWS-shaped input")

	// GCP-shaped with stale AWS residue: scrub. The composer JSON tag for
	// GCP CloudSQL is `gcp_cloudsql` (no underscore between cloud and SQL)
	// — that field's presence trips the cloud=GCP heuristic.
	mixed := pricingFromJSON(t, `{"_guidance_version":"v1","components":{
		"gcp_vpc":{"monthlyUSD":0},
		"gcp_cloudsql":{"monthlyUSD":42.00},
		"aws_lambda":{"monthlyUSD":1.87}
	}}`)
	mixed.Normalize()
	assert.Nil(t, mixed.Components.AWSLambda,
		"PricingData.Normalize must scrub AWS residue when GCP fields are present (cloud=GCP heuristic)")
	require.NotNil(t, mixed.Components.GCPCloudSQL,
		"GCP rows must survive the GCP-side scrub")
}

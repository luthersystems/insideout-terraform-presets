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
	t.Parallel()
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

// TestPricingItem_IsMissingSentinel locks the gap-detection contract: a
// sentinel emitted by MergePricing is distinguishable from an LLM-emitted
// PricingItem that happens to carry Status="missing". Detection requires
// BOTH the status string AND the #1434 marker substring in Details — checking
// status alone would mis-classify any LLM payload that uses the word
// "missing" for unrelated reasons. /review #1437 P1.
func TestPricingItem_IsMissingSentinel(t *testing.T) {
	t.Parallel()

	// Nil receiver is safe and reports false.
	var nilItem *PricingItem
	assert.False(t, nilItem.IsMissingSentinel(), "nil receiver must return false (no panic)")

	// Empty item — neither status nor marker — is not a sentinel.
	empty := &PricingItem{}
	assert.False(t, empty.IsMissingSentinel())

	// Status set but no marker in Details — NOT a sentinel. The whole
	// point of the helper is to distinguish merge-emitted sentinels from
	// LLM-emitted "missing" statuses for unrelated reasons.
	llmMissing := &PricingItem{
		Status:  PricingItemStatusMissing,
		Details: "LLM said this component is not applicable",
	}
	assert.False(t, llmMissing.IsMissingSentinel(),
		"matching status alone must not classify as sentinel — the marker substring "+
			"is what distinguishes merge-emitted from LLM-emitted missings")

	// Status missing AND marker present — IS a sentinel.
	sentinel := &PricingItem{
		Status:  PricingItemStatusMissing,
		Details: "fresh pricing omitted for aws_opensearch; " + PricingItemMissingDetailsMarker,
	}
	assert.True(t, sentinel.IsMissingSentinel())

	// Different status, marker present — defensive: not a sentinel.
	wrongStatus := &PricingItem{
		Status:  "weird",
		Details: PricingItemMissingDetailsMarker,
	}
	assert.False(t, wrongStatus.IsMissingSentinel())
}

// TestMergePricing_SentinelDetectableByExportedHelper pins the end-to-end
// contract: when MergePricing attaches a missing-reprice sentinel, calling
// IsMissingSentinel on the resulting PricingItem returns true. Locks the
// integration so a future refactor of setPricingSentinel cannot drift from
// the exported detection helper. /review #1437 P1.
func TestMergePricing_SentinelDetectableByExportedHelper(t *testing.T) {
	t.Parallel()

	prior := pricingFromJSON(t, `{"_guidance_version":"v1","components":{
		"aws_lambda":{"monthlyUSD":1.87}
	}}`)
	fresh := pricingFromJSON(t, `{"_guidance_version":"v1","components":{
		"aws_lambda":{"monthlyUSD":1.87}
	}}`)
	repriceSet := RepriceSet(map[ComponentKey]bool{KeyAWSOpenSearch: true})
	witness := Components{
		Cloud:         "AWS",
		AWSLambda:     boolPtr(true),
		AWSOpenSearch: boolPtr(true),
	}

	merged, _ := MergePricing(prior, fresh, repriceSet, witness)
	require.NotNil(t, merged)
	require.NotNil(t, merged.Components.AWSOpenSearch,
		"MergePricing must attach the sentinel when the witness confirms selection")
	assert.True(t, merged.Components.AWSOpenSearch.IsMissingSentinel(),
		"the attached sentinel must be detectable via the exported IsMissingSentinel helper")
}

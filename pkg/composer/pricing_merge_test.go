package composer

// Regression tests for luthersystems/reliable#1434 — pricing carry-forward
// across component add / remove / re-add and across config-driven repricing.
//
// Migrated from reliable per luthersystems/reliable#1437 PR-3 with the
// variadic `componentsOpt ...Components` argument flattened to an explicit
// `Components` parameter. Callers with no witness pass `Components{}`.
//
// These exercise `MergePricing` (the public surface of `ApplyCarryForward`)
// in isolation, no LLM and no DB. They pin the desired semantics that
//   (a) a removed component's prior price must not carry forward,
//   (b) a re-added component must be repriced from fresh, not carried,
//   (c) a config change for a kept component triggers repricing,
//   (d) the LLM fabricating an extra pricing row for an unselected component
//       must NOT be quietly merged.
//
// The actual prod failure mode (sess_v2_CnqUJ6NRJnLC) is that the fresh
// pricing payload comes back WITHOUT a re-added component (the LLM "forgot"
// to price it). That manifests two layers above this file
// (`calculate_current_costs` tool returning a payload with no `aws_opensearch`
// row); MergePricing's only job is then to surface the gap cleanly. These
// tests pin that surface so a future merge tweak doesn't paper over the LLM
// bug by silently carrying the prior price.

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pricingFromJSON parses the inline anonymous Components struct on
// PricingData by going through JSON. Fields are only constructible by the
// package itself so tests use this helper to build fixtures.
func pricingFromJSON(t *testing.T, payload string) *PricingData {
	t.Helper()
	var p PricingData
	require.NoError(t, json.Unmarshal([]byte(payload), &p))
	return &p
}

// boolPtrLocal — small wrapper that matches the reliable test fixture name
// so the ported assertions read the same. `boolPtr` already exists in
// types_test.go for package-level fixtures.
func boolPtrLocal(b bool) *bool { return &b }

// TestMergePricing_Issue1434_RemovedComponentClearsPriorPrice pins the
// carry-forward semantics for a removal: prior had aws_opensearch=$345.60;
// the user just removed opensearch; fresh pricing correctly omits it. The
// merged result must NOT carry the prior price forward (that would be the
// classic "deselected component still showing in cost") — the toggle diff
// puts the component in repriceSet, and the fresh nil wins.
//
// Today GREEN — locks the documented #921 invariant ("dropping a component
// silently kept its stale price" was the bug fixed).
func TestMergePricing_Issue1434_RemovedComponentClearsPriorPrice(t *testing.T) {
	prior := pricingFromJSON(t, `{"_guidance_version":"v1","components":{
		"aws_lambda":{"monthlyUSD":1.87},
		"aws_s3":{"monthlyUSD":2.30},
		"aws_opensearch":{"monthlyUSD":345.60}
	}}`)
	fresh := pricingFromJSON(t, `{"_guidance_version":"v1","components":{
		"aws_lambda":{"monthlyUSD":1.87},
		"aws_s3":{"monthlyUSD":2.30}
	}}`)
	repriceSet := RepriceSet(map[ComponentKey]bool{KeyAWSOpenSearch: true})

	merged, _ := MergePricing(prior, fresh, repriceSet, Components{})
	require.NotNil(t, merged)
	assert.Nil(t, merged.Components.AWSOpenSearch,
		"#1434/#921: a removed component's prior price must NOT carry forward — repriceSet wins and fresh is nil")
}

// TestMergePricing_Issue1434_ReAddRepricesNotCarries pins the re-add side:
// prior pricing happened during a turn that had opensearch; the user then
// removed it; then re-added on this turn. Fresh pricing comes back with a
// FRESH price for opensearch (the desired LLM behaviour). The merged result
// must use the fresh price, not carry-forward any historical price.
func TestMergePricing_Issue1434_ReAddRepricesNotCarries(t *testing.T) {
	prior := pricingFromJSON(t, `{"_guidance_version":"v1","components":{
		"aws_lambda":{"monthlyUSD":1.87},
		"aws_opensearch":{"monthlyUSD":345.60}
	}}`)
	fresh := pricingFromJSON(t, `{"_guidance_version":"v1","components":{
		"aws_lambda":{"monthlyUSD":1.87},
		"aws_opensearch":{"monthlyUSD":172.80,"details":"Half OCU reserved"}
	}}`)
	repriceSet := RepriceSet(map[ComponentKey]bool{KeyAWSOpenSearch: true})

	merged, _ := MergePricing(prior, fresh, repriceSet, Components{})
	require.NotNil(t, merged)
	require.NotNil(t, merged.Components.AWSOpenSearch)
	require.NotNil(t, merged.Components.AWSOpenSearch.MonthlyUSD)
	assert.InDelta(t, 172.80, *merged.Components.AWSOpenSearch.MonthlyUSD, 0.001,
		"#1434: fresh price for a re-added component must replace the prior price, not be shadowed by carry-forward")
}

// TestMergePricing_Issue1434_FreshOmitsRepriceComponent is the exact
// sess_v2_CnqUJ6NRJnLC failure shape: the LLM was asked to re-price after
// re-adding opensearch, but the tool returned a payload with NO
// aws_opensearch row at all. repriceSet has aws_opensearch (toggle change
// v_prev=false → v_current=true). Today `MergePricing` sees `repriceSet[k]=
// true` and `freshF.IsNil()` and — with the witness — attaches a missing-
// reprice sentinel so the gap is surfaced post-merge.
func TestMergePricing_Issue1434_FreshOmitsRepriceComponent(t *testing.T) {
	prior := pricingFromJSON(t, `{"_guidance_version":"v1","components":{
		"aws_lambda":{"monthlyUSD":1.87},
		"aws_s3":{"monthlyUSD":0.23}
	}}`)
	fresh := pricingFromJSON(t, `{"_guidance_version":"v1","components":{
		"aws_lambda":{"monthlyUSD":1.87},
		"aws_s3":{"monthlyUSD":0.23}
	}}`)
	repriceSet := RepriceSet(map[ComponentKey]bool{KeyAWSOpenSearch: true})

	// #1435 fix: thread a Components witness that confirms opensearch IS
	// selected this turn. Without it the merge can't distinguish "user
	// added it and LLM forgot" from "spurious reverse-pricing-dep entry"
	// — both shapes have prior+fresh nil. With the witness, the missing-
	// reprice sentinel fires.
	witness := Components{
		Cloud:         "AWS",
		AWSLambda:     boolPtrLocal(true),
		AWSS3:         boolPtrLocal(true),
		AWSOpenSearch: boolPtrLocal(true),
	}

	merged, stats := MergePricing(prior, fresh, repriceSet, witness)
	require.NotNil(t, merged)

	assert.NotNil(t, merged.Components.AWSOpenSearch,
		"#1434: when a component is in repriceSet but fresh omits it, MergePricing must surface "+
			"the gap (return error, mark stats, or attach a sentinel item) instead of silently producing "+
			"a pricing snapshot with no row for the selected component — this is the prod failure shape")
	assert.Equal(t, 1, stats.MissingReprices,
		"#1434: stats.MissingReprices must count the gap so the caller can log it")
}

// TestMergePricing_Issue1434_PhantomFreshPriceForUnselectedComponent pins
// the inverse: the LLM hallucinates a pricing row for a component that is
// NOT in the components selection. With the witness, MergePricing strips
// the phantom row so it can't survive into the merged snapshot.
func TestMergePricing_Issue1434_PhantomFreshPriceForUnselectedComponent(t *testing.T) {
	prior := pricingFromJSON(t, `{"_guidance_version":"v1","components":{
		"aws_lambda":{"monthlyUSD":1.87},
		"aws_opensearch":{"monthlyUSD":345.60}
	}}`)
	// User just removed opensearch. Fresh should be without it. But the
	// LLM hallucinated and kept pricing it anyway.
	fresh := pricingFromJSON(t, `{"_guidance_version":"v1","components":{
		"aws_lambda":{"monthlyUSD":1.87},
		"aws_opensearch":{"monthlyUSD":345.60,"details":"hallucinated by LLM"}
	}}`)
	repriceSet := RepriceSet(map[ComponentKey]bool{KeyAWSOpenSearch: true})

	// #1435 fix: MergePricing accepts a Components witness reflecting the
	// user's CURRENT selection (post-removal, opensearch is absent). The
	// merge strips any fresh row whose component is not selected — the
	// phantom doesn't survive.
	witness := Components{Cloud: "AWS", AWSLambda: boolPtrLocal(true)}

	merged, stats := MergePricing(prior, fresh, repriceSet, witness)
	require.NotNil(t, merged)

	assert.Nil(t, merged.Components.AWSOpenSearch,
		"#1434: a phantom pricing row for an unselected component must not survive into the merged result")
	assert.GreaterOrEqual(t, stats.PhantomsDropped, 1,
		"#1434: stats.PhantomsDropped must count the stripped phantom row")
}

// TestMergePricing_Issue1434_ConfigChangeDoesNotCarry pins the documented
// carry-forward escape hatch: when the user changes a kept component's
// config (e.g. Lambda memorySize 1024 → 2048), the component goes into the
// repriceSet and the fresh price wins. Original #921 mechanism, must keep
// working.
func TestMergePricing_Issue1434_ConfigChangeDoesNotCarry(t *testing.T) {
	prior := pricingFromJSON(t, `{"_guidance_version":"v1","components":{
		"aws_lambda":{"monthlyUSD":1.87},
		"aws_s3":{"monthlyUSD":0.23}
	}}`)
	fresh := pricingFromJSON(t, `{"_guidance_version":"v1","components":{
		"aws_lambda":{"monthlyUSD":3.53,"details":"Bumped memory to 2048MB"},
		"aws_s3":{"monthlyUSD":0.23}
	}}`)
	// Toggle didn't change, but Lambda config changed → repriceSet.
	repriceSet := RepriceSet(map[ComponentKey]bool{KeyAWSLambda: true})

	merged, _ := MergePricing(prior, fresh, repriceSet, Components{})
	require.NotNil(t, merged)
	require.NotNil(t, merged.Components.AWSLambda)
	require.NotNil(t, merged.Components.AWSLambda.MonthlyUSD)
	assert.InDelta(t, 3.53, *merged.Components.AWSLambda.MonthlyUSD, 0.001,
		"#1434: a config change for a kept component must trigger repricing, not carry-forward")

	// And S3 untouched → must carry forward (not a regression flag).
	require.NotNil(t, merged.Components.AWSS3)
	require.NotNil(t, merged.Components.AWSS3.MonthlyUSD)
	assert.InDelta(t, 0.23, *merged.Components.AWSS3.MonthlyUSD, 0.001,
		"S3 unchanged and untouched in this turn → carry-forward keeps the prior price stable")
}

// TestMergePricing_Issue1434_GuidanceVersionBustForcesFullReprice pins the
// other escape hatch: when the prior pricing was stamped under a different
// guidance version, the carry-forward gate fails and all of fresh wins.
func TestMergePricing_Issue1434_GuidanceVersionBustForcesFullReprice(t *testing.T) {
	prior := pricingFromJSON(t, `{"_guidance_version":"v0","components":{
		"aws_lambda":{"monthlyUSD":9.99}
	}}`)
	fresh := pricingFromJSON(t, `{"_guidance_version":"v1","components":{
		"aws_lambda":{"monthlyUSD":1.87}
	}}`)
	// Empty repriceSet — without the guidance bust, MergePricing would
	// carry forward $9.99. With the bust, fresh wins.
	repriceSet := RepriceSet(map[ComponentKey]bool{})

	merged, stats := ApplyCarryForward(prior, fresh, repriceSet, Components{})
	require.NotNil(t, merged)
	assert.True(t, stats.GuidanceBust, "prior under v0 must trip the bust gate when current is v1")
	require.NotNil(t, merged.Components.AWSLambda)
	require.NotNil(t, merged.Components.AWSLambda.MonthlyUSD)
	assert.InDelta(t, 1.87, *merged.Components.AWSLambda.MonthlyUSD, 0.001,
		"under guidance-bust, the prior price must NOT survive — fresh wins for every component")
}

// TestMergePricing_Issue1434_CrossCloudCarryForward captures cross-cloud
// pricing residual. Prior pricing was for an AWS stack; current turn
// switched to GCP. Repriced via toggles for the AWS components going off
// and GCP components coming on. Fresh has GCP-only rows. Expected merged:
// no AWS rows.
func TestMergePricing_Issue1434_CrossCloudCarryForward(t *testing.T) {
	prior := pricingFromJSON(t, `{"_guidance_version":"v1","components":{
		"aws_lambda":{"monthlyUSD":1.87},
		"aws_s3":{"monthlyUSD":0.23}
	}}`)
	// gcp_vpc presence is what flips PricingData.Normalize's cloud heuristic
	// to GCP, which is required to keep the GCP fields after the in-merge
	// Normalize pass. Without it Normalize defaults to AWS and nukes every
	// GCP row.
	fresh := pricingFromJSON(t, `{"_guidance_version":"v1","components":{
		"gcp_vpc":{"monthlyUSD":0},
		"gcp_cloud_functions":{"monthlyUSD":1.50},
		"gcp_gcs":{"monthlyUSD":0.20}
	}}`)
	repriceSet := RepriceSet(map[ComponentKey]bool{
		KeyAWSLambda:         true,
		KeyAWSS3:             true,
		KeyGCPVPC:            true,
		KeyGCPCloudFunctions: true,
		KeyGCPGCS:            true,
	})

	merged, _ := MergePricing(prior, fresh, repriceSet, Components{})
	require.NotNil(t, merged)
	assert.Nil(t, merged.Components.AWSLambda,
		"#1434: AWS Lambda price must not carry into a GCP-only stack")
	assert.Nil(t, merged.Components.AWSS3,
		"#1434: AWS S3 price must not carry into a GCP-only stack")
	require.NotNil(t, merged.Components.GCPCloudFunctions, "GCP fresh price should land")
	require.NotNil(t, merged.Components.GCPGCS, "GCP fresh price should land")
}

// TestMergePricing_Issue1434_SubtotalRecomputedAfterCarryForward pins that
// the merged result's `subtotalMonthlyUSD` reflects the merged components,
// not whatever subtotal happened to be on `prior` or `fresh`.
func TestMergePricing_Issue1434_SubtotalRecomputedAfterCarryForward(t *testing.T) {
	prior := pricingFromJSON(t, `{"_guidance_version":"v1","components":{
		"aws_lambda":{"monthlyUSD":1.87},
		"aws_opensearch":{"monthlyUSD":345.60}
	},"subtotalMonthlyUSD":347.47}`)
	fresh := pricingFromJSON(t, `{"_guidance_version":"v1","components":{
		"aws_lambda":{"monthlyUSD":1.87}
	},"subtotalMonthlyUSD":1.87}`)
	repriceSet := RepriceSet(map[ComponentKey]bool{KeyAWSOpenSearch: true})

	merged, _ := ApplyCarryForward(prior, fresh, repriceSet, Components{})
	require.NotNil(t, merged)
	require.NotNil(t, merged.SubtotalMonthlyUSD,
		"recomputeSubtotal must populate SubtotalMonthlyUSD on the merged result")
	assert.InDelta(t, 1.87, *merged.SubtotalMonthlyUSD, 0.001,
		"#1434: subtotal after removing opensearch must equal sum of remaining components ($1.87), "+
			"not the stale prior subtotal ($347.47) or the fresh subtotal computed before merge")
}

// TestMergePricing_Issue1434_NoComponentsAtAllReturnsNilSubtotal pins the
// empty-merge edge: fresh has no components, prior has none either, no
// repriceSet. The merged result must be defined (not panic) and its
// subtotal should reflect "no components priced" — zero.
func TestMergePricing_Issue1434_NoComponentsAtAllReturnsNilSubtotal(t *testing.T) {
	prior := pricingFromJSON(t, `{"_guidance_version":"v1","components":{}}`)
	fresh := pricingFromJSON(t, `{"_guidance_version":"v1","components":{}}`)
	repriceSet := RepriceSet(map[ComponentKey]bool{})

	merged, _ := ApplyCarryForward(prior, fresh, repriceSet, Components{})
	require.NotNil(t, merged)
	require.NotNil(t, merged.SubtotalMonthlyUSD)
	assert.InDelta(t, 0.0, *merged.SubtotalMonthlyUSD, 0.001,
		"empty merge must yield a zero subtotal, not nil or NaN")
}

// TestMergePricing_Issue1434_StatusOnlyItemSurvivesCarry pins an oft-
// overlooked shape: pricing items with `status:"Included"` and no
// `monthlyUSD`. The LLM uses these for "Serverless architecture — Included"
// rows and they must carry-forward correctly when nothing changed.
func TestMergePricing_Issue1434_StatusOnlyItemSurvivesCarry(t *testing.T) {
	prior := pricingFromJSON(t, `{"_guidance_version":"v1","components":{
		"architecture":{"status":"Included","details":"Serverless"},
		"aws_lambda":{"monthlyUSD":1.87}
	}}`)
	fresh := pricingFromJSON(t, `{"_guidance_version":"v1","components":{
		"aws_lambda":{"monthlyUSD":1.87}
	}}`)
	// Nothing changed — empty repriceSet.
	repriceSet := RepriceSet(map[ComponentKey]bool{})

	merged, _ := MergePricing(prior, fresh, repriceSet, Components{})
	require.NotNil(t, merged)
	require.NotNil(t, merged.Components.Architecture,
		"status-only 'Included' rows must carry-forward when unchanged")
	assert.Equal(t, "Included", merged.Components.Architecture.Status)
}

// =========================================================================
// New upstream-specific tests beyond the reliable port — lock the explicit-
// Components signature semantics that replaced the reliable variadic.
// =========================================================================

// TestMergePricing_ExplicitComponentsParam_NoWitnessUsesZero locks the
// no-witness behaviour of the upstream signature: passing the zero
// `Components{}` value MUST behave as "no witness" — no phantom-strip, no
// gap-surface, no panic. The reliable variadic relied on `len(componentsOpt)`
// to decide this; the upstream explicit-param design decodes a fully-zero
// Components instead. Locked here because a refactor that dropped the
// zero-witness short-circuit would silently start stripping every fresh row
// on every no-witness call.
func TestMergePricing_ExplicitComponentsParam_NoWitnessUsesZero(t *testing.T) {
	// fresh has a row that NO populated witness would say is selected — but
	// no witness is supplied, so the strip MUST NOT fire.
	prior := pricingFromJSON(t, `{"_guidance_version":"v1","components":{}}`)
	fresh := pricingFromJSON(t, `{"_guidance_version":"v1","components":{
		"aws_lambda":{"monthlyUSD":1.87},
		"aws_opensearch":{"monthlyUSD":345.60}
	}}`)
	repriceSet := RepriceSet(map[ComponentKey]bool{KeyAWSLambda: true})

	merged, stats := MergePricing(prior, fresh, repriceSet, Components{})
	require.NotNil(t, merged)
	// No phantom strip without a witness.
	require.NotNil(t, merged.Components.AWSLambda, "no-witness call must not strip fresh rows")
	require.NotNil(t, merged.Components.AWSOpenSearch, "no-witness call must not strip fresh rows")
	assert.Equal(t, 0, stats.PhantomsDropped, "no-witness call must report zero phantoms dropped")
	// No gap-surface either: repriceSet[KeyAWSLambda]=true and fresh has a row,
	// so this is the normal repriced path, not the sentinel path.
	assert.Equal(t, 0, stats.MissingReprices, "no-witness call must not emit gap sentinels")
}

// TestApplyCarryForward_ExplicitComponentsParam_FlowsToInner locks the wiring
// of the explicit Components param from ApplyCarryForward through to the
// inner MergePricing strip/gap pipeline. A populated witness MUST result in
// phantom-strip behaviour on the bust path AND on the merge path — the
// reliable variadic threaded both, and the upstream explicit param must too.
func TestApplyCarryForward_ExplicitComponentsParam_FlowsToInner(t *testing.T) {
	// Build a prior under the current guidance version (no bust), with the
	// phantom row already cleared from prior — the strip is concerned with
	// fresh's hallucinated rows.
	prior := pricingFromJSON(t, `{"_guidance_version":"v1","components":{
		"aws_lambda":{"monthlyUSD":1.87}
	}}`)
	fresh := pricingFromJSON(t, `{"_guidance_version":"v1","components":{
		"aws_lambda":{"monthlyUSD":1.87},
		"aws_opensearch":{"monthlyUSD":345.60,"details":"hallucinated by LLM"}
	}}`)
	repriceSet := RepriceSet(map[ComponentKey]bool{KeyAWSLambda: true})

	// Witness says only Lambda is selected; opensearch on fresh is a phantom.
	witness := Components{Cloud: "AWS", AWSLambda: boolPtrLocal(true)}

	merged, stats := ApplyCarryForward(prior, fresh, repriceSet, witness)
	require.NotNil(t, merged)
	assert.Nil(t, merged.Components.AWSOpenSearch,
		"phantom strip MUST fire when ApplyCarryForward passes a populated witness through to MergePricing")
	assert.GreaterOrEqual(t, stats.PhantomsDropped, 1,
		"phantom strip count MUST be surfaced on the stats out of ApplyCarryForward")
	require.NotNil(t, merged.Components.AWSLambda)
}


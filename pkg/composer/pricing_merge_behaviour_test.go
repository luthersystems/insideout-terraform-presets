package composer

// Behavioural tests for the carry-forward merge family (MergePricing /
// ApplyCarryForward / ShouldCarryForward / recomputeSubtotal / deepCopyPricing).
//
// Migrated from luthersystems/reliable's chatv2 package per the reliable#1437
// follow-up — the 10 #1434 regressions already live alongside upstream in
// pricing_merge_test.go (the `Issue1434_*` tests); these add the broader
// behavioural coverage (atomic-backups carry/reprice, prior immutability,
// guidance bust, first-turn edges, top-level field preservation, deep-copy
// symmetry, JSON round-trip, reverse-pricing-dep RDS→Backups, GCP backups,
// legacy-field clearing) so the composer package owns the FULL test surface
// for the rules it implements.
//
// Style note: these ports use the t.Errorf style of the original reliable
// suite for minimal-diff porting. Other tests in this file use testify
// (assert/require) per upstream convention; both styles are valid.

import (
	"bytes"
	"encoding/json"
	"testing"
)

// newPrice returns a PricingItem with a float MonthlyUSD for readable fixtures.
func newPrice(usd float64) *PricingItem {
	return &PricingItem{MonthlyUSD: &usd}
}

// assertStatsConsistent enforces the MergeStats invariants: Total must equal
// Carried+Repriced, and when merged is available Total must also equal the
// count of non-nil per-component items. Call from every merge test so a
// mutation to any `stats.Total++` branch is caught.
func assertStatsConsistent(t *testing.T, stats MergeStats, merged *PricingData) {
	t.Helper()
	if stats.Total != stats.Carried+stats.Repriced {
		t.Errorf("stats.Total (%d) != Carried (%d) + Repriced (%d)", stats.Total, stats.Carried, stats.Repriced)
	}
	if merged != nil {
		if got := countComponentItems(&merged.Components); got != stats.Total {
			t.Errorf("stats.Total (%d) != non-nil components in merged output (%d)", stats.Total, got)
		}
	}
}

// priceUSD safely reads MonthlyUSD, returning 0 if nil.
func priceUSD(item *PricingItem) float64 {
	if item == nil || item.MonthlyUSD == nil {
		return 0
	}
	return *item.MonthlyUSD
}

// buildPricing constructs a compact AWS PricingData fixture. The guidance
// version is always set to the current value so ShouldCarryForward(prior)
// returns true — use buildStalePricing for mismatched versions.
func buildPricing(cloudfront, cognito, secrets, cloudwatchMon, lambda float64) *PricingData {
	pd := &PricingData{
		Currency:        "USD",
		GuidanceVersion: PriceGuidanceVersion,
	}
	pd.Components.AWSCloudFront = newPrice(cloudfront)
	pd.Components.AWSCognito = newPrice(cognito)
	pd.Components.AWSSecretsManager = newPrice(secrets)
	pd.Components.AWSCloudWatchMonitoring = newPrice(cloudwatchMon)
	pd.Components.AWSLambda = newPrice(lambda)
	return pd
}

// buildStalePricing constructs the same fixture but stamps an old guidance
// version so ShouldCarryForward(prior) returns false.
func buildStalePricing(cloudfront, cognito, secrets, cloudwatchMon, lambda float64) *PricingData {
	pd := buildPricing(cloudfront, cognito, secrets, cloudwatchMon, lambda)
	pd.GuidanceVersion = "v-stale"
	return pd
}

// TestMergePricing_AC_IdenticalConfigNoJitter is the headline regression test
// from the ticket: when nothing changed (empty repriceSet), the fresh LLM
// output — even with wildly different numbers — must not leak through.
func TestMergePricing_AC_IdenticalConfigNoJitter(t *testing.T) {
	prior := buildPricing(8.60, 5.00, 2.00, 3.50, 4.00)
	// The LLM re-priced and hallucinated different numbers for components
	// that didn't change — the production bug from #921.
	fresh := buildPricing(9.50, 0.00, 0.40, 7.25, 4.00)

	expectedCarried := countComponentItems(&prior.Components)

	merged, stats := MergePricing(prior, fresh, map[ComponentKey]bool{}, Components{})

	if got := priceUSD(merged.Components.AWSCloudFront); got != 8.60 {
		t.Errorf("AWSCloudFront: want 8.60 (carried), got %.2f", got)
	}
	if got := priceUSD(merged.Components.AWSCognito); got != 5.00 {
		t.Errorf("AWSCognito: want 5.00 (carried), got %.2f", got)
	}
	if got := priceUSD(merged.Components.AWSSecretsManager); got != 2.00 {
		t.Errorf("AWSSecretsManager: want 2.00 (carried), got %.2f", got)
	}
	if got := priceUSD(merged.Components.AWSCloudWatchMonitoring); got != 3.50 {
		t.Errorf("AWSCloudWatchMonitoring: want 3.50 (carried), got %.2f", got)
	}
	if stats.Repriced != 0 {
		t.Errorf("stats.Repriced: want 0, got %d", stats.Repriced)
	}
	if stats.Carried != expectedCarried {
		t.Errorf("stats.Carried: want %d (from fixture), got %d (stats=%+v)", expectedCarried, stats.Carried, stats)
	}
	assertStatsConsistent(t, stats, merged)
}

func TestMergePricing_CloudFrontChange_RepricesOnlyCloudFront(t *testing.T) {
	prior := buildPricing(8.60, 5.00, 2.00, 3.50, 4.00)
	fresh := buildPricing(9.50, 0.00, 0.40, 7.25, 4.00)

	changed := map[ComponentKey]bool{KeyAWSCloudfront: true}
	merged, stats := MergePricing(prior, fresh, RepriceSet(changed), Components{})

	if got := priceUSD(merged.Components.AWSCloudFront); got != 9.50 {
		t.Errorf("AWSCloudFront should reprice to 9.50, got %.2f", got)
	}
	if got := priceUSD(merged.Components.AWSCognito); got != 5.00 {
		t.Errorf("AWSCognito should carry 5.00, got %.2f", got)
	}
	if got := priceUSD(merged.Components.AWSSecretsManager); got != 2.00 {
		t.Errorf("AWSSecretsManager should carry 2.00, got %.2f", got)
	}
	if stats.Repriced != 1 {
		t.Errorf("stats.Repriced: want 1, got %d", stats.Repriced)
	}
	if stats.Carried != 4 {
		t.Errorf("stats.Carried: want 4, got %d", stats.Carried)
	}
	assertStatsConsistent(t, stats, merged)
}

func TestMergePricing_LambdaChange_ForcesCloudWatchReprice(t *testing.T) {
	prior := buildPricing(8.60, 5.00, 2.00, 3.50, 4.00)
	fresh := buildPricing(9.99, 0.00, 0.00, 15.75, 12.00)

	changed := map[ComponentKey]bool{KeyAWSLambda: true}
	merged, stats := MergePricing(prior, fresh, RepriceSet(changed), Components{})
	defer assertStatsConsistent(t, stats, merged)

	if got := priceUSD(merged.Components.AWSLambda); got != 12.00 {
		t.Errorf("AWSLambda should reprice to 12.00, got %.2f", got)
	}
	if got := priceUSD(merged.Components.AWSCloudWatchMonitoring); got != 15.75 {
		t.Errorf("AWSCloudWatchMonitoring should reprice (Lambda dep), got %.2f", got)
	}
	if got := priceUSD(merged.Components.AWSCloudFront); got != 8.60 {
		t.Errorf("AWSCloudFront should carry 8.60, got %.2f", got)
	}
	if got := priceUSD(merged.Components.AWSCognito); got != 5.00 {
		t.Errorf("AWSCognito should carry 5.00, got %.2f", got)
	}
	if got := priceUSD(merged.Components.AWSSecretsManager); got != 2.00 {
		t.Errorf("AWSSecretsManager should carry 2.00, got %.2f", got)
	}
}

func TestMergePricing_FirstTurn_PriorNil(t *testing.T) {
	fresh := buildPricing(8.60, 5.00, 2.00, 3.50, 4.00)
	expectedRepriced := countComponentItems(&fresh.Components)

	merged, stats := MergePricing(nil, fresh, nil, Components{})

	if merged != fresh {
		t.Errorf("first turn: expected fresh returned unchanged")
	}
	if stats.Carried != 0 {
		t.Errorf("first turn: expected 0 carried, got %d", stats.Carried)
	}
	if stats.Repriced != expectedRepriced {
		t.Errorf("first turn: expected %d repriced, got %d", expectedRepriced, stats.Repriced)
	}
	assertStatsConsistent(t, stats, merged)
}

func TestMergePricing_FreshNil(t *testing.T) {
	prior := buildPricing(8.60, 5.00, 2.00, 3.50, 4.00)
	merged, stats := MergePricing(prior, nil, nil, Components{})
	if merged != nil {
		t.Errorf("expected nil merged when fresh is nil")
	}
	if stats != (MergeStats{}) {
		t.Errorf("expected zero stats when fresh is nil, got %+v", stats)
	}
	assertStatsConsistent(t, stats, merged)
}

func TestMergePricing_NewComponentAddedThisTurn_NilRepriceSet(t *testing.T) {
	prior := &PricingData{Currency: "USD", GuidanceVersion: PriceGuidanceVersion}
	prior.Components.AWSCloudFront = newPrice(8.60)

	fresh := &PricingData{Currency: "USD"}
	fresh.Components.AWSCloudFront = newPrice(9.99) // LLM jitter
	fresh.Components.AWSRDS = newPrice(50.00)       // newly added

	merged, stats := MergePricing(prior, fresh, nil, Components{})

	if got := priceUSD(merged.Components.AWSCloudFront); got != 8.60 {
		t.Errorf("AWSCloudFront should carry 8.60, got %.2f", got)
	}
	if got := priceUSD(merged.Components.AWSRDS); got != 50.00 {
		t.Errorf("AWSRDS (new) should use fresh 50.00, got %.2f", got)
	}
	if stats.Carried != 1 {
		t.Errorf("stats.Carried: want 1, got %d", stats.Carried)
	}
	if stats.Repriced != 1 {
		t.Errorf("stats.Repriced: want 1 (new RDS), got %d", stats.Repriced)
	}
	assertStatsConsistent(t, stats, merged)
}

// TestMergePricing_NewComponentAddedThisTurn_NonNilRepriceSet verifies the
// defensive "new component not in repriceSet" branch when the caller has
// explicitly computed a non-nil reprice set that omits the new component.
// A mutation from `!freshF.IsNil()` to `true` at pricing_merge.go:~130 would
// be caught here because it would double-count.
func TestMergePricing_NewComponentAddedThisTurn_NonNilRepriceSet(t *testing.T) {
	prior := &PricingData{Currency: "USD", GuidanceVersion: PriceGuidanceVersion}
	prior.Components.AWSCloudFront = newPrice(8.60)

	fresh := &PricingData{Currency: "USD"}
	fresh.Components.AWSCloudFront = newPrice(9.99)
	fresh.Components.AWSRDS = newPrice(50.00)

	// Explicit non-nil repriceSet that excludes AWSRDS.
	repriceSet := map[ComponentKey]bool{KeyAWSSQS: true}
	merged, stats := MergePricing(prior, fresh, repriceSet, Components{})

	if got := priceUSD(merged.Components.AWSCloudFront); got != 8.60 {
		t.Errorf("AWSCloudFront should carry 8.60, got %.2f", got)
	}
	if got := priceUSD(merged.Components.AWSRDS); got != 50.00 {
		t.Errorf("AWSRDS (new, not in repriceSet) should use fresh 50.00, got %.2f", got)
	}
	if stats.Carried != 1 {
		t.Errorf("stats.Carried: want 1 (CloudFront), got %d", stats.Carried)
	}
	if stats.Repriced != 1 {
		t.Errorf("stats.Repriced: want 1 (new RDS), got %d", stats.Repriced)
	}
}

// TestMergePricing_RepriceSetHitFreshNil covers the `repriceSet[key] &&
// freshF.IsNil()` branch: no stat increment, fresh stays nil.
func TestMergePricing_RepriceSetHitFreshNil(t *testing.T) {
	prior := &PricingData{Currency: "USD", GuidanceVersion: PriceGuidanceVersion}
	prior.Components.AWSCloudFront = newPrice(8.60)

	// Fresh has no CloudFront (LLM returned nil for it), even though config changed.
	fresh := &PricingData{Currency: "USD"}

	repriceSet := map[ComponentKey]bool{KeyAWSCloudfront: true}
	merged, stats := MergePricing(prior, fresh, repriceSet, Components{})

	if merged.Components.AWSCloudFront != nil {
		t.Errorf("expected CloudFront to stay nil (reprice hit + fresh nil), got %+v", merged.Components.AWSCloudFront)
	}
	if stats.Repriced != 0 {
		t.Errorf("stats.Repriced: want 0 (fresh was nil), got %d", stats.Repriced)
	}
	if stats.Carried != 0 {
		t.Errorf("stats.Carried: want 0 (in reprice set), got %d", stats.Carried)
	}
	assertStatsConsistent(t, stats, merged)
}

func TestMergePricing_RoundTripSerialization(t *testing.T) {
	prior := buildPricing(8.60, 5.00, 2.00, 3.50, 4.00)
	fresh := buildPricing(9.50, 0.00, 0.40, 7.25, 4.00)

	changed := map[ComponentKey]bool{KeyAWSCloudfront: true}
	merged, stats := MergePricing(prior, fresh, RepriceSet(changed), Components{})
	assertStatsConsistent(t, stats, merged)
	merged.GuidanceVersion = PriceGuidanceVersion // caller stamps post-merge

	b, err := json.Marshal(merged)
	if err != nil {
		t.Fatalf("marshal merged: %v", err)
	}
	// The private guidance marker must be persisted (needed for bust detection).
	if !bytes.Contains(b, []byte(`"_guidance_version":"`+PriceGuidanceVersion+`"`)) {
		t.Errorf("expected guidance version in marshaled output, got: %s", string(b))
	}
	var back PricingData
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("unmarshal merged: %v", err)
	}
	if got := priceUSD(back.Components.AWSCloudFront); got != 9.50 {
		t.Errorf("round-trip CloudFront: want 9.50, got %.2f", got)
	}
	if got := priceUSD(back.Components.AWSCognito); got != 5.00 {
		t.Errorf("round-trip Cognito: want 5.00, got %.2f", got)
	}
	if back.GuidanceVersion != PriceGuidanceVersion {
		t.Errorf("round-trip guidance: want %q, got %q", PriceGuidanceVersion, back.GuidanceVersion)
	}
}

func TestMergePricing_TopLevelFieldsPreserved(t *testing.T) {
	prior := buildPricing(8.60, 5.00, 2.00, 3.50, 4.00)
	fresh := buildPricing(9.50, 0.00, 0.40, 7.25, 4.00)
	fresh.Currency = "USD"

	merged, stats := MergePricing(prior, fresh, nil, Components{})
	if merged.Currency != "USD" {
		t.Errorf("Currency: want USD, got %q", merged.Currency)
	}
	assertStatsConsistent(t, stats, merged)
}

// TestMergePricing_PriorImmutability asserts MergePricing does not mutate
// prior or alias its pricing pointers into the merged output. Guards against
// a regression where future refactors could share *PricingItem (or inner
// *float64) across both, which would let downstream mutations silently
// corrupt the persisted prior snapshot.
//
// Three mutation vectors are exercised:
//  1. Replacing the pointer `merged.X.MonthlyUSD = &newVal`  — catches aliasing
//     at the *PricingItem level.
//  2. Mutating through the pointee `*merged.X.MonthlyUSD = newVal` — catches a
//     shallow struct-copy (e.g. `priorCopy := *prior`) that shares inner
//     *float64 pointers even though the outer PricingItem structs differ.
//  3. Top-level subtotal pointer identity — catches a regression that would
//     share a *float64 between prior and merged's SubtotalMonthlyUSD.
func TestMergePricing_PriorImmutability(t *testing.T) {
	prior := buildPricing(8.60, 5.00, 2.00, 3.50, 4.00)
	priorCognitoItem := prior.Components.AWSCognito
	priorCognitoMonthlyPtr := prior.Components.AWSCognito.MonthlyUSD
	originalCognitoUSD := priceUSD(prior.Components.AWSCognito)

	fresh := buildPricing(9.50, 0.00, 0.40, 7.25, 4.00)
	merged, _ := MergePricing(prior, fresh, nil, Components{})

	// Vector 0: *PricingItem pointer identity. A shallow struct copy
	// (priorCopy := *prior) would share pointers here.
	if merged.Components.AWSCognito == priorCognitoItem {
		t.Fatalf("merged.AWSCognito aliases prior.AWSCognito — deep-copy expected")
	}
	// Vector 1: inner *float64 pointer identity. A "copy the struct but keep
	// the inner pointers" regression would alias MonthlyUSD. JSON round-trip
	// always allocates fresh floats, so these must be distinct.
	if merged.Components.AWSCognito.MonthlyUSD == priorCognitoMonthlyPtr {
		t.Fatalf("merged.AWSCognito.MonthlyUSD aliases prior's *float64 — deep-copy expected")
	}

	// Vector 2: replace pointer on merged; prior must be untouched.
	newVal := 99.99
	merged.Components.AWSCognito.MonthlyUSD = &newVal
	if priceUSD(prior.Components.AWSCognito) != originalCognitoUSD {
		t.Errorf("vec2: prior.AWSCognito mutated through pointer replacement: prior=%.2f, expected=%.2f",
			priceUSD(prior.Components.AWSCognito), originalCognitoUSD)
	}

	// Vector 3: mutate through a fresh pointee that merged alone points at;
	// prior must still be unaffected. Catches "shared struct, distinct outer
	// pointer" regressions.
	mergedVal := 42.42
	merged.Components.AWSCognito = &PricingItem{MonthlyUSD: &mergedVal}
	mergedVal = 77.77
	if priceUSD(prior.Components.AWSCognito) != originalCognitoUSD {
		t.Errorf("vec3: prior.AWSCognito mutated through pointee: prior=%.2f, expected=%.2f",
			priceUSD(prior.Components.AWSCognito), originalCognitoUSD)
	}

	// Vector 4: top-level subtotal aliasing. MergePricing itself doesn't set
	// subtotal, but if the deep-copy boundary ever leaked the *float64 the
	// bug would surface at this invariant.
	if prior.SubtotalMonthlyUSD != nil && merged.SubtotalMonthlyUSD != nil &&
		prior.SubtotalMonthlyUSD == merged.SubtotalMonthlyUSD {
		t.Errorf("merged.SubtotalMonthlyUSD aliases prior's — deep-copy expected")
	}
}

// ============================== Backups ===================================

// TestMergePricing_Backups_CarryForward verifies the *PricingBackups sub-struct
// is carried forward as an atomic unit when not in the reprice set.
func TestMergePricing_Backups_CarryForward(t *testing.T) {
	prior := &PricingData{Currency: "USD", GuidanceVersion: PriceGuidanceVersion}
	prior.Components.AWSRDS = newPrice(50.00)
	prior.Components.AWSBackups = &PricingBackups{
		Rds: newPrice(5.00),
		S3:  newPrice(1.00),
	}

	fresh := &PricingData{Currency: "USD"}
	fresh.Components.AWSRDS = newPrice(50.00)
	// LLM jitter on backups — different numbers that should NOT leak through.
	fresh.Components.AWSBackups = &PricingBackups{
		Rds: newPrice(99.00),
		S3:  newPrice(99.00),
	}

	merged, stats := MergePricing(prior, fresh, nil, Components{})

	if merged.Components.AWSBackups == nil {
		t.Fatal("AWSBackups was dropped")
	}
	if got := priceUSD(merged.Components.AWSBackups.Rds); got != 5.00 {
		t.Errorf("Backups.Rds: want 5.00 (carried), got %.2f", got)
	}
	if got := priceUSD(merged.Components.AWSBackups.S3); got != 1.00 {
		t.Errorf("Backups.S3: want 1.00 (carried), got %.2f", got)
	}
	if stats.Repriced != 0 {
		t.Errorf("stats.Repriced: want 0, got %d", stats.Repriced)
	}
	assertStatsConsistent(t, stats, merged)
}

// TestMergePricing_Backups_RepricedWhenRDSChanges exercises the reverse pricing
// dep: an RDS change forces AWSBackups into the reprice set (backups scale with
// what they back up). The whole *PricingBackups sub-struct is treated atomically.
func TestMergePricing_Backups_RepricedWhenRDSChanges(t *testing.T) {
	prior := &PricingData{Currency: "USD", GuidanceVersion: PriceGuidanceVersion}
	prior.Components.AWSRDS = newPrice(50.00)
	prior.Components.AWSBackups = &PricingBackups{
		Rds: newPrice(5.00),
		S3:  newPrice(1.00),
	}

	fresh := &PricingData{Currency: "USD"}
	fresh.Components.AWSRDS = newPrice(80.00) // bigger RDS
	fresh.Components.AWSBackups = &PricingBackups{
		Rds: newPrice(8.00), // scaled
		S3:  newPrice(1.00),
	}

	changed := map[ComponentKey]bool{KeyAWSRDS: true}
	repriceSet := RepriceSet(changed)
	if !repriceSet[KeyAWSBackups] {
		t.Fatal("repriceSet should include AWSBackups when RDS changes (reverse pricing dep)")
	}

	merged, stats := MergePricing(prior, fresh, repriceSet, Components{})

	if got := priceUSD(merged.Components.AWSBackups.Rds); got != 8.00 {
		t.Errorf("Backups.Rds: want 8.00 (repriced), got %.2f", got)
	}
	if got := priceUSD(merged.Components.AWSRDS); got != 80.00 {
		t.Errorf("AWSRDS: want 80.00 (repriced), got %.2f", got)
	}
	assertStatsConsistent(t, stats, merged)
}

func TestMergePricing_GCPBackups_CarryForward(t *testing.T) {
	prior := &PricingData{Currency: "USD", GuidanceVersion: PriceGuidanceVersion}
	prior.Components.GCPBackups = &GCPPricingBackups{
		CloudSQL: newPrice(6.00),
		GCS:      newPrice(2.00),
	}

	fresh := &PricingData{Currency: "USD"}
	fresh.Components.GCPBackups = &GCPPricingBackups{
		CloudSQL: newPrice(99.00),
		GCS:      newPrice(99.00),
	}

	merged, stats := MergePricing(prior, fresh, nil, Components{})
	if got := priceUSD(merged.Components.GCPBackups.CloudSQL); got != 6.00 {
		t.Errorf("GCPBackups.CloudSQL: want 6.00 (carried), got %.2f", got)
	}
	assertStatsConsistent(t, stats, merged)
}

// ============================ Subtotal ====================================

// TestRecomputeSubtotal covers the subtotal recomputation that keeps
// merged.SubtotalMonthlyUSD consistent with the line items after carry-forward.
func TestRecomputeSubtotal(t *testing.T) {
	pd := &PricingData{}
	pd.Components.AWSCloudFront = newPrice(10.0)
	pd.Components.AWSCognito = newPrice(5.0)
	pd.Components.AWSBackups = &PricingBackups{
		Rds: newPrice(3.0),
		S3:  newPrice(1.0),
	}

	recomputeSubtotal(pd)

	if pd.SubtotalMonthlyUSD == nil {
		t.Fatal("SubtotalMonthlyUSD should be non-nil after recompute")
	}
	want := 10.0 + 5.0 + 3.0 + 1.0
	if *pd.SubtotalMonthlyUSD != want {
		t.Errorf("Subtotal: want %.2f (items + backups children), got %.2f", want, *pd.SubtotalMonthlyUSD)
	}
}

func TestRecomputeSubtotal_Empty(t *testing.T) {
	pd := &PricingData{}
	recomputeSubtotal(pd)
	if pd.SubtotalMonthlyUSD == nil || *pd.SubtotalMonthlyUSD != 0 {
		t.Errorf("empty PricingData: want subtotal=0, got %v", pd.SubtotalMonthlyUSD)
	}
}

// TestApplyCarryForward_SubtotalConsistencyAfterMerge is the P0 regression:
// the LLM's subtotal must be overwritten so it matches the merged line items.
func TestApplyCarryForward_SubtotalConsistencyAfterMerge(t *testing.T) {
	prior := buildPricing(8.60, 5.00, 2.00, 3.50, 4.00) // sum=23.10
	fresh := buildPricing(9.50, 0.00, 0.40, 7.25, 4.00) // LLM jitter; sum=21.15
	llmSubtotal := 21.15
	fresh.SubtotalMonthlyUSD = &llmSubtotal

	merged, _ := ApplyCarryForward(prior, fresh, nil, Components{}) // nothing changed → all carried

	if merged.SubtotalMonthlyUSD == nil {
		t.Fatal("expected recomputed subtotal")
	}
	// Expected: sum of prior's line items (since all carried).
	expected := 8.60 + 5.00 + 2.00 + 3.50 + 4.00
	if diff := *merged.SubtotalMonthlyUSD - expected; diff < -0.001 || diff > 0.001 {
		t.Errorf("Subtotal after carry-forward: want %.2f (sum of line items), got %.2f",
			expected, *merged.SubtotalMonthlyUSD)
	}
}

// ========================= ApplyCarryForward =============================

func TestApplyCarryForward_GuidanceMismatch_FullReprice(t *testing.T) {
	prior := buildStalePricing(8.60, 5.00, 2.00, 3.50, 4.00) // "v-stale"
	fresh := buildPricing(9.50, 0.00, 0.40, 7.25, 4.00)

	merged, stats := ApplyCarryForward(prior, fresh, map[ComponentKey]bool{}, Components{})

	if !stats.GuidanceBust {
		t.Errorf("expected GuidanceBust=true on stale prior")
	}
	if stats.Carried != 0 {
		t.Errorf("stats.Carried: want 0 on bust, got %d", stats.Carried)
	}
	// Every number in merged must come from fresh (not prior).
	if got := priceUSD(merged.Components.AWSCloudFront); got != 9.50 {
		t.Errorf("CloudFront: want 9.50 (fresh), got %.2f", got)
	}
	if got := priceUSD(merged.Components.AWSCognito); got != 0.00 {
		t.Errorf("Cognito: want 0.00 (fresh), got %.2f", got)
	}
}

func TestApplyCarryForward_FirstTurn_NoGuidanceBust(t *testing.T) {
	fresh := buildPricing(8.60, 5.00, 2.00, 3.50, 4.00)
	merged, stats := ApplyCarryForward(nil, fresh, nil, Components{})

	if stats.GuidanceBust {
		t.Errorf("first turn should NOT set GuidanceBust (prior was nil, not mismatched)")
	}
	if stats.Carried != 0 {
		t.Errorf("first turn: expected 0 carried, got %d", stats.Carried)
	}
	if merged == nil {
		t.Fatal("expected merged != nil")
	}
	if merged.SubtotalMonthlyUSD == nil {
		t.Errorf("expected subtotal to be computed on first turn")
	}
}

func TestApplyCarryForward_FreshNil(t *testing.T) {
	prior := buildPricing(8.60, 5.00, 2.00, 3.50, 4.00)
	merged, stats := ApplyCarryForward(prior, nil, nil, Components{})
	if merged != nil {
		t.Errorf("expected nil merged when fresh is nil")
	}
	if (stats != MergeStats{}) {
		t.Errorf("expected zero stats when fresh is nil, got %+v", stats)
	}
}

func TestApplyCarryForward_MatchingGuidance_DelegatesToMerge(t *testing.T) {
	prior := buildPricing(8.60, 5.00, 2.00, 3.50, 4.00)
	fresh := buildPricing(9.50, 0.00, 0.40, 7.25, 4.00)

	merged, stats := ApplyCarryForward(prior, fresh, map[ComponentKey]bool{}, Components{})

	if stats.GuidanceBust {
		t.Errorf("matching guidance should NOT set GuidanceBust")
	}
	if got := priceUSD(merged.Components.AWSCloudFront); got != 8.60 {
		t.Errorf("CloudFront should carry 8.60, got %.2f", got)
	}
	assertStatsConsistent(t, stats, merged)
}

// ========================= ShouldCarryForward =============================

func TestShouldCarryForward_VersionMatching(t *testing.T) {
	if ShouldCarryForward(nil) {
		t.Errorf("nil prior must not carry forward")
	}
	prior := &PricingData{}
	if ShouldCarryForward(prior) {
		t.Errorf("prior with empty guidance must not carry forward")
	}
	prior.GuidanceVersion = "v-something-old"
	if ShouldCarryForward(prior) {
		t.Errorf("prior with mismatched guidance must not carry forward")
	}
	prior.GuidanceVersion = PriceGuidanceVersion
	if !ShouldCarryForward(prior) {
		t.Errorf("prior with matching guidance should carry forward")
	}
}

// ===================== Backups atomic-unit semantics ======================

// TestMergePricing_Backups_AtomicCarry_DifferentChildren pins the intended
// "*PricingBackups is a single atomic unit" contract. Prior has {Rds, S3};
// fresh has {DynamoDB, EC2}. With no reprice set, carry-forward must take
// prior's struct wholesale — merged has {Rds, S3} (not a per-child merge).
//
// This test would fail if someone refactored MergePricing to recurse into
// *PricingBackups and merge children individually.
func TestMergePricing_Backups_AtomicCarry_DifferentChildren(t *testing.T) {
	prior := &PricingData{Currency: "USD", GuidanceVersion: PriceGuidanceVersion}
	prior.Components.AWSBackups = &PricingBackups{
		Rds: newPrice(5.00),
		S3:  newPrice(1.00),
	}

	fresh := &PricingData{Currency: "USD"}
	fresh.Components.AWSBackups = &PricingBackups{
		DynamoDB: newPrice(7.00),
		EC2:      newPrice(3.00),
	}

	merged, stats := MergePricing(prior, fresh, nil, Components{})

	if merged.Components.AWSBackups == nil {
		t.Fatal("AWSBackups was dropped during atomic carry")
	}
	// Expect prior's children, fresh's children nil (atomic swap, no merge).
	if got := priceUSD(merged.Components.AWSBackups.Rds); got != 5.00 {
		t.Errorf("Backups.Rds: want 5.00 (prior's child), got %.2f", got)
	}
	if got := priceUSD(merged.Components.AWSBackups.S3); got != 1.00 {
		t.Errorf("Backups.S3: want 1.00 (prior's child), got %.2f", got)
	}
	if merged.Components.AWSBackups.DynamoDB != nil {
		t.Errorf("Backups.DynamoDB: want nil (fresh child should NOT leak), got %+v",
			merged.Components.AWSBackups.DynamoDB)
	}
	if merged.Components.AWSBackups.EC2 != nil {
		t.Errorf("Backups.EC2: want nil (fresh child should NOT leak), got %+v",
			merged.Components.AWSBackups.EC2)
	}
	assertStatsConsistent(t, stats, merged)
}

// TestMergePricing_Backups_AtomicReprice_DifferentChildren is the converse
// of the above: when Backups IS in the reprice set, fresh's struct wins
// wholesale. Prior's children must NOT leak into the merged output.
func TestMergePricing_Backups_AtomicReprice_DifferentChildren(t *testing.T) {
	prior := &PricingData{Currency: "USD", GuidanceVersion: PriceGuidanceVersion}
	prior.Components.AWSBackups = &PricingBackups{
		Rds: newPrice(5.00),
		S3:  newPrice(1.00),
	}

	fresh := &PricingData{Currency: "USD"}
	fresh.Components.AWSBackups = &PricingBackups{
		DynamoDB: newPrice(7.00),
		EC2:      newPrice(3.00),
	}

	repriceSet := map[ComponentKey]bool{KeyAWSBackups: true}
	merged, stats := MergePricing(prior, fresh, repriceSet, Components{})

	if merged.Components.AWSBackups == nil {
		t.Fatal("AWSBackups was dropped during atomic reprice")
	}
	// Expect fresh's children, prior's children nil.
	if got := priceUSD(merged.Components.AWSBackups.DynamoDB); got != 7.00 {
		t.Errorf("Backups.DynamoDB: want 7.00 (fresh's child), got %.2f", got)
	}
	if got := priceUSD(merged.Components.AWSBackups.EC2); got != 3.00 {
		t.Errorf("Backups.EC2: want 3.00 (fresh's child), got %.2f", got)
	}
	if merged.Components.AWSBackups.Rds != nil {
		t.Errorf("Backups.Rds: want nil (prior child should NOT leak into repriced), got %+v",
			merged.Components.AWSBackups.Rds)
	}
	if merged.Components.AWSBackups.S3 != nil {
		t.Errorf("Backups.S3: want nil (prior child should NOT leak into repriced), got %+v",
			merged.Components.AWSBackups.S3)
	}
	assertStatsConsistent(t, stats, merged)
}

// ====================== Legacy-field clearing =============================

// TestMergePricing_FreshLegacyFieldsCleared exercises the documented
// invariant at pricing_merge.go: "fresh is Normalize()'d first so LLM-populated
// legacy fields don't inflate the repriced count." A mutation that removes
// the Normalize() call would double-count (both legacy + canonical).
func TestMergePricing_FreshLegacyFieldsCleared(t *testing.T) {
	prior := &PricingData{Currency: "USD", GuidanceVersion: PriceGuidanceVersion}
	prior.Components.AWSCloudFront = newPrice(8.60)

	// LLM emits BOTH legacy "cloudfront" AND canonical "aws_cloudfront".
	fresh := &PricingData{Currency: "USD"}
	fresh.Components.AWSCloudFront = newPrice(9.00)
	fresh.Components.CloudFront = newPrice(9.99) // legacy — Normalize() drops this

	merged, stats := MergePricing(prior, fresh, nil, Components{})

	if merged.Components.CloudFront != nil {
		t.Errorf("merged.Components.CloudFront (legacy): want nil after Normalize, got %+v",
			merged.Components.CloudFront)
	}
	if got := priceUSD(merged.Components.AWSCloudFront); got != 8.60 {
		t.Errorf("AWSCloudFront: want 8.60 (carried), got %.2f", got)
	}
	// stats.Total must only count the canonical entry (1), never double.
	if stats.Total != 1 {
		t.Errorf("stats.Total: want 1 (legacy dropped by Normalize), got %d (stats=%+v)",
			stats.Total, stats)
	}
	assertStatsConsistent(t, stats, merged)
}

// ================== JSON round-trip symmetry for deepCopy =================

// TestDeepCopyPricing_Symmetry verifies the JSON-roundtrip deep copy returns
// an equal PricingData so future additions of unexported fields or custom
// MarshalJSON implementations that would silently drop data fail loudly.
func TestDeepCopyPricing_Symmetry(t *testing.T) {
	original := buildPricing(8.60, 5.00, 2.00, 3.50, 4.00)
	original.Components.AWSBackups = &PricingBackups{
		Rds: newPrice(5.00),
		S3:  newPrice(1.00),
	}
	subtotal := 23.10
	original.SubtotalMonthlyUSD = &subtotal

	copyA := deepCopyPricing(original)
	if copyA == nil {
		t.Fatal("deepCopyPricing returned nil for a valid input")
	}

	// Independent pointers at every level.
	if copyA == original {
		t.Errorf("deepCopyPricing returned the same pointer")
	}
	if copyA.Components.AWSCloudFront == original.Components.AWSCloudFront {
		t.Errorf("deepCopyPricing did not copy AWSCloudFront pointer")
	}
	if copyA.SubtotalMonthlyUSD == original.SubtotalMonthlyUSD {
		t.Errorf("deepCopyPricing did not copy SubtotalMonthlyUSD pointer")
	}

	// Semantically equal (post another JSON round-trip for comparison).
	origBytes, _ := json.Marshal(original)
	copyBytes, _ := json.Marshal(copyA)
	if !bytes.Equal(origBytes, copyBytes) {
		t.Errorf("deep-copy dropped data:\n  original: %s\n  copy:     %s", origBytes, copyBytes)
	}
}

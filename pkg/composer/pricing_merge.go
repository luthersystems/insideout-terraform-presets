package composer

// pricing_merge.go owns the carry-forward merge that combines a prior
// PricingData with a freshly-LLM-returned PricingData, gated by the pricing
// guidance version and the per-component repriceSet. Migrated from reliable
// per luthersystems/reliable#1437 PR-3.
//
// The Components witness — formerly a `...Components` variadic in reliable —
// is now an explicit `Components` parameter on MergePricing and
// ApplyCarryForward. Callers that have no witness pass `Components{}`;
// ComponentSelected(&Components{}, key) returns false for every key, so the
// witness-driven gap-surface (missing-reprice sentinel) and phantom-strip
// become no-ops in the no-witness case. This makes the witness argument
// non-optional at the type level — explicit > variadic per the #1437 design.
//
// The actual rules (what carries, what is repriced, what triggers a gap
// sentinel) are unchanged from the reliable implementation. Selection
// predicates now use the canonical ComponentSelected from coherence.go
// instead of an in-file duplicate.

import (
	"encoding/json"
	"reflect"
)

// MergeStats reports the outcome of a carry-forward merge. Emitted in the
// pricing log line so we can measure the LLM-cost / jitter reduction (#921 AC-3).
type MergeStats struct {
	Total           int  // per-component pricing items present in merged output
	Repriced        int  // items taken from the fresh LLM response
	Carried         int  // items copied from the prior version
	GuidanceBust    bool // true when the prior's guidance version didn't match; full reprice used
	MissingReprices int  // #1434: components in repriceSet+selected but absent from prior AND fresh
	PhantomsDropped int  // #1434: fresh rows for components NOT selected in witness (stripped)
}

// ApplyCarryForward is the single entrypoint the call-site should use. It
// enforces the guidance-version gate, then delegates to MergePricing when
// carry-forward is safe. On gate-bust, returns fresh unchanged with
// GuidanceBust=true so the caller can log the condition.
//
// The `components` witness threads the current components selection into the
// merge so MergePricing can (a) strip phantom rows for unselected components
// and (b) surface gaps where a selected+repriced component is missing from
// both prior and fresh (#1434). Callers with no witness pass `Components{}`
// — every selection predicate returns false on the zero value, which makes
// the witness-driven steps no-ops without special-casing.
func ApplyCarryForward(prior, fresh *PricingData, repriceSet map[ComponentKey]bool, components Components) (*PricingData, MergeStats) {
	if !ShouldCarryForward(prior) {
		stats := MergeStats{}
		if fresh != nil {
			// #1434: strip phantom rows on the bust path too — fresh wins,
			// but rows for unselected components are still hallucinations.
			stripPhantomPricing(&fresh.Components, components, &stats)
			n := countComponentItems(&fresh.Components)
			stats.Total = n
			stats.Repriced = n
		}
		if prior != nil {
			stats.GuidanceBust = true
		}
		if fresh != nil {
			recomputeSubtotal(fresh)
		}
		return fresh, stats
	}
	merged, stats := MergePricing(prior, fresh, repriceSet, components)
	if merged != nil {
		recomputeSubtotal(merged)
	}
	return merged, stats
}

// MergePricing applies carry-forward: for every per-component pricing item in
// `fresh`, if the component is NOT in repriceSet, overwrite fresh's item with
// prior's (when prior has one). Items in repriceSet keep their fresh value.
//
// This eliminates LLM-jitter on untouched components without discarding the
// LLM's holistic view (the LLM still sees the full stack when it prices).
//
// Pass-through behavior:
//   - fresh == nil → returns nil, zero stats (caller handles error upstream).
//   - prior == nil → every fresh item is counted as repriced; no-op merge.
//
// The guidance-version check is NOT enforced here — use ApplyCarryForward for
// the gated path; MergePricing is exposed for testing the merge mechanics in
// isolation.
//
// The `components` witness (#1434):
//   - strips phantom rows for components the witness says are NOT selected,
//     and
//   - attaches a sentinel `PricingItem{Status:"missing"}` for components the
//     witness says ARE selected but where both prior and fresh have no row
//     (and the key is in `repriceSet`).
//
// A zero-value witness (`Components{}`) reports every key as unselected, so
// both steps become no-ops: phantom-strip clears nothing (because there's
// nothing to compare against intentionally) and gap-surface never fires. The
// signature is non-optional so the design intent is visible at the call site
// — passing `Components{}` is the explicit "I have no witness" path.
//
// Implementation details:
//   - fresh is Normalize()'d first so LLM-populated legacy fields don't inflate
//     the repriced count.
//   - prior is deep-copied via JSON so the merged output never shares
//     PricingItem pointers with the caller's prior (defence against accidental
//     aliasing that could corrupt the persisted prior on mutation).
//   - The walk uses reflection over PricingData.Components (mirrors the
//     composer.DiffConfigs pattern) so new fields are handled automatically.
func MergePricing(prior, fresh *PricingData, repriceSet map[ComponentKey]bool, components Components) (*PricingData, MergeStats) {
	var stats MergeStats
	if fresh == nil {
		return nil, stats
	}
	fresh.Normalize()
	// #1434: strip phantom rows BEFORE the merge walk. When the Components
	// witness says a component is NOT selected, any pricing row for it on
	// fresh is a hallucination — drop it so downstream layers can't render
	// it. A zero-value witness no-ops on every field. Updates
	// stats.PhantomsDropped.
	stripPhantomPricing(&fresh.Components, components, &stats)

	if prior == nil {
		n := countComponentItems(&fresh.Components)
		stats.Repriced = n
		stats.Total = n
		return fresh, stats
	}

	priorCopy := deepCopyPricing(prior)
	if priorCopy == nil {
		// Marshal failure (should never happen); degrade to full reprice.
		n := countComponentItems(&fresh.Components)
		stats.Repriced = n
		stats.Total = n
		return fresh, stats
	}

	priorC := reflect.ValueOf(&priorCopy.Components).Elem()
	freshC := reflect.ValueOf(&fresh.Components).Elem()
	t := priorC.Type()
	if priorC.Type() != freshC.Type() {
		// Struct layouts diverged — skip merge rather than risk a Set panic.
		n := countComponentItems(&fresh.Components)
		stats.Repriced = n
		stats.Total = n
		return fresh, stats
	}

	for i := 0; i < t.NumField(); i++ {
		tag := JSONTagName(t.Field(i))
		if tag == "" {
			continue
		}
		priorF := priorC.Field(i)
		freshF := freshC.Field(i)
		if priorF.Kind() != reflect.Pointer || freshF.Kind() != reflect.Pointer {
			continue
		}
		if priorF.Type() != freshF.Type() {
			continue
		}

		key := ComponentKey(tag)
		if repriceSet[key] {
			if !freshF.IsNil() {
				stats.Repriced++
				stats.Total++
				continue
			}
			// #1434: repriceSet[k]=true AND freshF is nil. Two shapes:
			//   - prior has it → user removed; correct drop. Leave nil.
			//   - prior also nil → either (a) component selected this turn
			//     and fresh forgot it (the prod failure shape), or (b) a
			//     spurious entry in repriceSet (e.g. reverse-pricing-dep
			//     expansion includes a component not in the stack — see
			//     KeyAWSCloudWatchLogs in PricingDependencies[Lambda]).
			//     The gap-surface sentinel ONLY fires when the Components
			//     witness confirms the component is selected; otherwise
			//     we'd over-report on stacks that never had the component.
			if !priorF.IsNil() {
				continue
			}
			if !ComponentSelected(&components, key) {
				continue
			}
			setPricingSentinel(freshF, key)
			stats.MissingReprices++
			stats.Total++
			continue
		}

		// Carry-forward when prior has a value for this component.
		if !priorF.IsNil() {
			freshF.Set(priorF)
			stats.Carried++
			stats.Total++
			continue
		}

		// Prior had nothing; fresh may or may not have a value. If fresh has
		// one, count it as repriced (new component the differ didn't flag).
		if !freshF.IsNil() {
			stats.Repriced++
			stats.Total++
		}
	}
	return fresh, stats
}

// stripPhantomPricing walks the reflected per-component pricing fields and
// nils out any field whose component key is NOT selected in the witness.
// Updates stats.PhantomsDropped for each row dropped.
//
// No-witness short-circuit: a fully-zero `Components` (the value callers pass
// to mean "I have no witness") carries no information to drive a strip — so
// the function early-returns. Without this guard, every fresh row would be
// dropped on a `Components{}` call, which is the opposite of the no-witness
// intent. With any populated field on the witness (Cloud, AWSLambda, …)
// stripPhantomPricing proceeds normally.
//
// #1434.
func stripPhantomPricing(comps any, witness Components, stats *MergeStats) {
	if isZeroComponents(witness) {
		return
	}
	v := reflect.ValueOf(comps).Elem()
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		tag := JSONTagName(t.Field(i))
		if tag == "" {
			continue
		}
		f := v.Field(i)
		if f.Kind() != reflect.Pointer || f.IsNil() {
			continue
		}
		key := ComponentKey(tag)
		if ComponentSelected(&witness, key) {
			continue
		}
		f.Set(reflect.Zero(f.Type()))
		if stats != nil {
			stats.PhantomsDropped++
		}
	}
}

// isZeroComponents reports whether the witness has NO selection fields set —
// i.e. the literal `Components{}`. Cheap reflective check; only used by
// stripPhantomPricing to distinguish "explicit empty stack" from "caller
// didn't supply a witness".
func isZeroComponents(c Components) bool {
	v := reflect.ValueOf(c)
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := v.Field(i)
		switch f.Kind() {
		case reflect.String:
			if f.String() != "" {
				return false
			}
		case reflect.Pointer:
			if !f.IsNil() {
				return false
			}
		}
	}
	return true
}

// setPricingSentinel sets the given reflected *PricingItem field to a
// fresh `PricingItem{Status:"missing", ...}` so the caller can detect
// post-merge that the LLM omitted pricing for a selected+repriced
// component. Embeds the component key in the details for downstream log
// correlation. Used by MergePricing when prior/fresh both lack a row.
// #1434.
func setPricingSentinel(field reflect.Value, key ComponentKey) {
	if field.Kind() != reflect.Pointer {
		return
	}
	elemT := field.Type().Elem()
	if elemT != reflect.TypeOf(PricingItem{}) {
		// PricingBackups sub-struct etc — skip the sentinel for those.
		return
	}
	sentinel := PricingItem{
		Status:  "missing",
		Details: "fresh pricing omitted for " + string(key) + "; needs reprice (#1434)",
	}
	field.Set(reflect.ValueOf(&sentinel))
}

// ShouldCarryForward returns true if carry-forward merging is safe given the
// prior version's stamped guidance version. When the prior was priced under a
// different guidance, old numbers may be wrong and must be re-computed.
func ShouldCarryForward(prior *PricingData) bool {
	if prior == nil {
		return false
	}
	return prior.GuidanceVersion == PriceGuidanceVersion
}

// countComponentItems returns the number of non-nil per-component pricing
// pointer fields on the Components struct.
func countComponentItems(c any) int {
	v := reflect.ValueOf(c).Elem()
	t := v.Type()
	n := 0
	for i := 0; i < t.NumField(); i++ {
		if JSONTagName(t.Field(i)) == "" {
			continue
		}
		f := v.Field(i)
		if f.Kind() != reflect.Pointer {
			continue
		}
		if !f.IsNil() {
			n++
		}
	}
	return n
}

// deepCopyPricing returns an independent copy of a PricingData via a
// JSON round-trip. Returns nil on marshal failure (caller treats as absent).
func deepCopyPricing(p *PricingData) *PricingData {
	if p == nil {
		return nil
	}
	data, err := json.Marshal(p)
	if err != nil {
		return nil
	}
	out := &PricingData{}
	if err := json.Unmarshal(data, out); err != nil {
		return nil
	}
	return out
}

// recomputeSubtotal walks every *PricingItem (including the children of the
// Backups sub-structs) and assigns the sum of non-nil MonthlyUSD values to
// pd.SubtotalMonthlyUSD. Must be called after any mutation to Components so
// the subtotal stays consistent with line items (#921 P0 correctness fix).
func recomputeSubtotal(pd *PricingData) {
	if pd == nil {
		return
	}
	var total float64
	walkPricingItems(&pd.Components, func(item *PricingItem) {
		if item != nil && item.MonthlyUSD != nil {
			total += *item.MonthlyUSD
		}
	})
	pd.SubtotalMonthlyUSD = &total
}

// walkPricingItems walks a PricingData.Components (anonymous struct) value
// and calls visit on every *PricingItem it finds — including the children of
// nested sub-structs like *PricingBackups / *GCPPricingBackups.
func walkPricingItems(c any, visit func(*PricingItem)) {
	v := reflect.ValueOf(c)
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return
	}
	t := v.Type()
	pricingItemType := reflect.TypeOf((*PricingItem)(nil))
	for i := 0; i < t.NumField(); i++ {
		f := v.Field(i)
		if f.Kind() != reflect.Pointer || f.IsNil() {
			continue
		}
		// Direct *PricingItem fields.
		if f.Type() == pricingItemType {
			visit(f.Interface().(*PricingItem))
			continue
		}
		// Sub-struct pointers (Backups variants): recurse into their fields.
		if f.Type().Elem().Kind() == reflect.Struct {
			walkPricingItems(f.Interface(), visit)
		}
	}
}

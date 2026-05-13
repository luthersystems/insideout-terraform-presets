package awsdiscover

import "github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/progress"

// DiscoverArgs is the per-call input shape consumed by the aggregator's
// DiscoverTypes and every per-service Discoverer.Discover. Bundling the
// inputs into a struct (instead of positional args) keeps the per-service
// signature stable when future fields land — e.g. #289 gap-#6 may add
// IncludeUnsupported, #289 gap-#10 may add a permissions probe handle.
//
// Callers populate Project (the stack project tag/label prefix passed to
// every per-service prefix or label filter), Regions (zero-or-more AWS
// regions; empty means "use the configured-region of the aws.Config"
// for back-compat with single --region invocations), TagSelectors (the
// operator-supplied AND-conjunction filter — see TagSelector), and
// AccountID (one STS GetCallerIdentity per run, threaded through so
// per-service code can stamp Identity.AccountID without re-deriving).
//
// Emitter (#295) carries the streaming-progress sink. Per-service code
// always calls methods on a non-nil Emitter; the aggregator resolves a
// nil Emitter to progress.NopEmitter{} once at the top of DiscoverTypes
// so per-service code never has to nil-check.
//
// DiscoverArgs is intentionally NOT exported via pkg/insideout-import/...
// — it lives in the cloud-specific aggregator package so the public
// registry surface stays dep-free for downstream consumers (reliable
// repo's importer wizard).
type DiscoverArgs struct {
	Project      string
	Regions      []string
	TagSelectors []TagSelector
	AccountID    string
	Emitter      progress.Emitter

	// rgtCache is the package-internal RGT prefetch result threaded
	// through DiscoverTypes (#406). Per-type discoverers that opt into
	// the unified RGT path read this via the package-private accessor
	// to skip their own ListResources fan-out. Stays unexported so the
	// public DiscoverArgs surface is unchanged for external callers,
	// and so tests that construct DiscoverArgs directly default to
	// nil cache (per-type list fallback).
	rgtCache *rgtCache
}

// withRGTCache returns a copy of args with rgtCache set. Used once at
// the top of AWSDiscoverer.DiscoverTypes after Prefetch returns.
func (a DiscoverArgs) withRGTCache(c *rgtCache) DiscoverArgs {
	a.rgtCache = c
	return a
}

// RGTCacheForCFN returns the prefetched ARNs for (region, cfnType), or
// (nil, false) when no cache exists or this bucket missed. Per-type
// discoverers (cloudControlDiscoverer) consult this before their own
// ListResources path. A nil cache (no Prefetch ran, e.g. no
// TagSelectors / Project set) always returns ok=false so callers fall
// back gracefully.
func (a DiscoverArgs) RGTCacheForCFN(region, cfnType string) ([]arnInfo, bool) {
	return a.rgtCache.ForCFN(region, cfnType)
}

// RGTCacheForGlobalCFN returns the de-duplicated union of prefetched
// ARNs for cfnType across every region bucket. Used by global-service
// discoverers (IAM, CloudFront, Route53) whose ARNs RGT surfaces in
// every region's response.
func (a DiscoverArgs) RGTCacheForGlobalCFN(cfnType string) ([]arnInfo, bool) {
	return a.rgtCache.ForGlobalCFN(cfnType)
}

// TagSelector is a single operator-supplied tag-equality clause. The
// match semantics are case-sensitive equality on both Key and Value;
// substring / prefix / regex variants are deliberately not supported
// (the reliable wizard's mockup at components/import/types.ts:69
// presents tag selectors as `key=value` strings and the
// case-equality model is the simplest contract that matches that UX).
//
// Multiple TagSelectors form an AND conjunction — see MatchesAll.
type TagSelector struct {
	Key   string
	Value string
}

// Matches reports whether the given tag map contains a key-value pair
// that exactly matches this selector. A nil tag map never matches.
func (s TagSelector) Matches(tags map[string]string) bool {
	if tags == nil {
		return false
	}
	v, ok := tags[s.Key]
	return ok && v == s.Value
}

// MatchesAll reports whether the given tag map satisfies every selector
// (AND conjunction). An empty selectors slice always matches — used by
// per-service discoverers as the no-filter fast path. A nil tag map
// matches only when selectors is empty.
func MatchesAll(tags map[string]string, selectors []TagSelector) bool {
	if len(selectors) == 0 {
		return true
	}
	for _, s := range selectors {
		if !s.Matches(tags) {
			return false
		}
	}
	return true
}

// emitterOrNop returns args.Emitter when non-nil, otherwise a NopEmitter
// (#295). Per-service Discover bodies call this once at the top to avoid
// nil-checking in every emit call site. The aggregator
// (AWSDiscoverer.DiscoverTypes) already defaults a nil Emitter to NopEmitter
// before fanning out, but per-service unit tests construct DiscoverArgs
// directly and won't go through the aggregator — without this fallback
// every existing test would have to set Emitter explicitly.
func emitterOrNop(e progress.Emitter) progress.Emitter {
	if e == nil {
		return progress.NopEmitter{}
	}
	return e
}

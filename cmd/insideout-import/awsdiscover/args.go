package awsdiscover

import (
	"sort"
	"strings"
	"time"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/progress"
)

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

	// PerTypeTimeout, when > 0, bounds the wall time a single per-type
	// Discoverer.Discover call may consume inside DiscoverTypes's
	// errgroup. On expiry the aggregator records a structured warn log
	// (`reason=per_type_timeout type=<t> elapsed_ms=<n>`) and emits an
	// empty result slice for that type — siblings keep running and the
	// overall call returns a partial result rather than failing the
	// whole gather. Zero means "no per-type bound" (back-compat: the
	// pre-#1787 behavior where one slow / stalled SDK call held the
	// whole gather hostage until the caller's outer deadline fired).
	//
	// Why an explicit bound rather than relying on the parent
	// `ctx` deadline: a parent deadline cancels every sibling
	// simultaneously, which throws away their work. A per-type bound
	// fences off the slow type while letting siblings finish, which is
	// the right shape for a best-effort discovery survey.
	PerTypeTimeout time.Duration

	// ParentScope, when non-empty, restricts parent-scoped child discovery
	// to a fixed set of selected parent identifiers per parent CloudFormation
	// type — the #739 selection-closure scoping fix. It is keyed by the
	// parent's CloudFormation type (e.g. "AWS::S3::Bucket") and lists the
	// parent identifiers the operator selected (e.g. the bucket names of the
	// selected aws_s3_bucket resources).
	//
	// When set, parent-scoped discoverers (the SDK-only sub-resource
	// discoverer's ListParents path and the Cloud Control discoverer's
	// ParentLister path) use these identifiers DIRECTLY as the parent set
	// instead of issuing an account-wide parent enumeration
	// (s3:ListBuckets, logs:DescribeLogGroups, …). This both scopes the
	// closure to the selected parents and removes the need for account-wide
	// list permissions. An empty/absent ParentScope preserves the
	// pre-#739 account-wide sweep (top-level discovery surveys, the local
	// CLI's full account scan, and any parent type not represented in the
	// scope all still enumerate account-wide).
	ParentScope ParentScope

	// rgtCache is the package-internal RGT prefetch result threaded
	// through DiscoverTypes (#406). Per-type discoverers that opt into
	// the unified RGT path read this via the package-private accessor
	// to skip their own ListResources fan-out. Stays unexported so the
	// public DiscoverArgs surface is unchanged for external callers,
	// and so tests that construct DiscoverArgs directly default to
	// nil cache (per-type list fallback).
	rgtCache *rgtCache
}

// ParentScope maps a parent CloudFormation type to the set of selected parent
// identifiers a closure run should restrict child discovery to. See
// DiscoverArgs.ParentScope. Construct with NewParentScope.
type ParentScope map[string][]string

// NewParentScope builds a ParentScope from (parentCFNType, identifier) pairs,
// de-duplicating identifiers per type and dropping empties. Returns nil when
// no usable pair is supplied so callers can leave DiscoverArgs.ParentScope
// at its zero value (which means "no scoping — account-wide sweep").
func NewParentScope(byCFNType map[string][]string) ParentScope {
	out := ParentScope{}
	for cfnType, ids := range byCFNType {
		cfnType = strings.TrimSpace(cfnType)
		if cfnType == "" {
			continue
		}
		seen := map[string]struct{}{}
		var deduped []string
		for _, id := range ids {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			deduped = append(deduped, id)
		}
		if len(deduped) == 0 {
			continue
		}
		sort.Strings(deduped)
		out[cfnType] = deduped
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// scopedParents returns the selected parent identifiers for parentCFNType and
// true when ParentScope restricts this type, otherwise (nil, false) so the
// caller falls back to its account-wide enumeration. A nil/empty ParentScope
// always returns (nil, false).
func (a DiscoverArgs) scopedParents(parentCFNType string) ([]string, bool) {
	if len(a.ParentScope) == 0 {
		return nil, false
	}
	ids, ok := a.ParentScope[strings.TrimSpace(parentCFNType)]
	if !ok || len(ids) == 0 {
		return nil, false
	}
	return append([]string(nil), ids...), true
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

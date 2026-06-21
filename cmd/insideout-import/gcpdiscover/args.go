package gcpdiscover

import (
	"sort"
	"strings"

	"github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/progress"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// DiscoverArgs is the per-call input shape consumed by the aggregator's
// DiscoverTypes. Mirrors awsdiscover.DiscoverArgs minus AccountID
// (GCP uses the projectID stored on the *GCPDiscoverer; the GCP path
// has no equivalent of an STS GetCallerIdentity round-trip per run).
//
// Regions populates the Cloud Asset query's location filter. Zero
// regions ⇒ no location clause (asset-API "all locations"). One ⇒
// "location:r1". Two or more ⇒ "(location:r1 OR location:r2 OR ...)".
//
// TagSelectors append `labels.<k>:<v>` clauses to the asset query. Per
// the asset query language the `:` operator is substring-match, not
// strict equality — same caveat as the existing labels.project:<v>
// filter the aggregator already emits.
//
// Emitter (#295) carries the streaming-progress sink. The aggregator
// resolves a nil Emitter to progress.NopEmitter{} once at the top of
// DiscoverTypes so per-asset translation never has to nil-check.
type DiscoverArgs struct {
	Project      string
	Regions      []string
	TagSelectors []TagSelector
	Emitter      progress.Emitter

	// ParentScope, when non-empty, restricts CAI enumeration to the
	// operator's selected parents during a selection-closure run (#777,
	// the GCP analog of awsdiscover.DiscoverArgs.ParentScope from #739/#770).
	//
	// It maps a parent Cloud Asset *asset type* (e.g.
	// "storage.googleapis.com/Bucket") to the set of selected parents of
	// that type, each carrying the name the InsideOut inspector attributes
	// it by and the GCP location it lives in (ScopedParent). DiscoverTypes
	// then narrows the per-bucket SearchAllResources query for any scoped
	// asset type to a `name:(<p1> OR <p2> ...)` clause over exactly those
	// parents instead of issuing the project-wide `labels.project:<stack>`
	// sweep — turning O(project) Cloud Asset reads into O(selected-parents)
	// reads (and dropping the broad project-wide read scope the sweep
	// needs).
	//
	// Keyed by ASSET type, not Terraform type: DiscoverTypes already
	// partitions discoverers into SearchAllResources buckets by AssetType,
	// so an asset-type key lets the scope plug straight into that seam
	// (mirroring how the AWS ParentScope keys by CloudFormation type — the
	// type the AWS discoverers route through).
	//
	// CRITICAL (mirrors the AWS (empty, true) contract): a scoped type
	// whose asset type is present in ParentScope but has ZERO selected
	// parents is SKIPPED — its asset type is dropped from every
	// SearchAllResources call rather than swept project-wide. The engine's
	// mergeClosureResources filter would discard a project-wide sweep's
	// extra rows anyway, but skipping the enumeration is the whole point of
	// #777: no broad read, no O(project) cost.
	//
	// An empty/absent ParentScope preserves the legacy project-wide sweep
	// (the local CLI's full scan and top-level discovery are unchanged).
	ParentScope ParentScope

	// OnTypeDiscovered, when non-nil, is invoked by DiscoverTypes exactly
	// once per requested type AS THAT TYPE COMPLETES, carrying the type's
	// discovered resources. It is the GCP twin of
	// awsdiscover.DiscoverArgs.OnTypeDiscovered (reliable#2060) — the
	// per-type RESULTS counterpart to the count-only
	// TypeProgressEmitter.TypeDone — so the reliable streaming discover path
	// can consume one DiscoverTypes call instead of fanning out single-type
	// calls to observe per-type results.
	//
	// Contract (mirrors the AWS twin):
	//
	//   - INVOKED ONCE PER REQUESTED TYPE, including a type that yielded no
	//     resources (delivered with an EMPTY, non-nil slice), so a consumer
	//     driving a progress denominator off the callbacks still advances to
	//     100%.
	//
	//   - SERIALIZED. DiscoverTypes serializes callback invocations under an
	//     internal mutex, so the callback needs no locking of its own.
	//     Invocations fire in COMPLETION order: CAI-backed types tick in a
	//     burst as the bulk SearchAllResources translation lands, then
	//     non-CAI types tick one at a time as their per-service listers
	//     return. The flattened slice DiscoverTypes RETURNS is unchanged.
	//
	//   - The delivered slice is an ISOLATED SNAPSHOT (fresh slice,
	//     value-copied elements, cloned NativeIDs), as with the AWS twin, so a
	//     consumer may retain and process it asynchronously. Unlike AWS, the
	//     GCP path runs NO post-completion cross-type augmentation pass —
	//     address/parent resolution happens inline in FromAsset via the shared
	//     addressBook during the CAI translation loop, before delivery — so
	//     the snapshot already carries the final field values that appear in
	//     the returned slice.
	//
	//   - FAILURE SEMANTICS: not invoked after DiscoverTypes returns an
	//     error. The GCP path is largely sequential after the bulk CAI
	//     search, so on an error mid-scan, types translated before the
	//     failure MAY already have fired — the consumer must tolerate a
	//     partial set of callbacks followed by a non-nil error. No type ever
	//     fires more than once.
	//
	// Nil OnTypeDiscovered is the back-compat default (CLI / mars unchanged).
	OnTypeDiscovered func(tfType string, resources []imported.ImportedResource)
}

// TagSelector is a single operator-supplied label-equality clause. See
// awsdiscover.TagSelector for the conjunction semantics; the GCP path
// applies selectors at query-time (server-side via Cloud Asset) rather
// than as a post-filter, so the AND of multiple clauses is the asset
// API's responsibility.
type TagSelector struct {
	Key   string
	Value string
}

// ScopedParent is one selected parent in a ParentScope: the Name the CAI
// query filters on (the parent's short resource name, e.g. a bucket name
// or keyring name) paired with the GCP Location it lives in. The GCP twin
// of awsdiscover.ScopedParent (#777).
//
// Unlike the AWS path — which loops per region and uses Region to decide
// which parents to fetch in each pass — the GCP CAI path issues a single
// SearchAllResources call per ScopeStyle bucket and expresses location in
// the query string. Location is therefore informational on GCP today (it
// rides along so a future location-narrowed `name:` + `location:` query
// can use it, and so the closure adapter can carry the parent's location
// through), and the per-parent `name:` clause is what actually scopes the
// read to the selected parents.
type ScopedParent struct {
	Name     string
	Location string
}

// ParentScope maps a parent Cloud Asset asset type (e.g.
// "storage.googleapis.com/Bucket") to the set of selected parents a
// closure run should restrict CAI enumeration to. See
// DiscoverArgs.ParentScope. Construct with NewParentScope.
//
// Keying by asset type (not Terraform type) lets the scope plug straight
// into DiscoverTypes' existing per-AssetType SearchAllResources bucketing.
type ParentScope map[string][]ScopedParent

// NewParentScope builds a ParentScope from (parentAssetType -> ScopedParent)
// pairs, de-duplicating parents per type by (name, location), trimming
// whitespace, dropping empties, and sorting by (name, location). Returns
// nil when no usable pair is supplied so callers can leave
// DiscoverArgs.ParentScope at its zero value (which means "no scoping —
// project-wide sweep"). Mirrors awsdiscover.NewParentScope's normalization
// so the two clouds' scope constructors behave identically (#777).
func NewParentScope(byAssetType map[string][]ScopedParent) ParentScope {
	out := ParentScope{}
	for assetType, parents := range byAssetType {
		assetType = strings.TrimSpace(assetType)
		if assetType == "" {
			continue
		}
		type key struct{ name, location string }
		seen := map[key]struct{}{}
		var deduped []ScopedParent
		for _, p := range parents {
			name := strings.TrimSpace(p.Name)
			if name == "" {
				continue
			}
			location := strings.TrimSpace(p.Location)
			k := key{name: name, location: location}
			if _, ok := seen[k]; ok {
				continue
			}
			seen[k] = struct{}{}
			deduped = append(deduped, ScopedParent{Name: name, Location: location})
		}
		if len(deduped) == 0 {
			continue
		}
		sort.Slice(deduped, func(i, j int) bool {
			if deduped[i].Name != deduped[j].Name {
				return deduped[i].Name < deduped[j].Name
			}
			return deduped[i].Location < deduped[j].Location
		})
		out[assetType] = deduped
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// scopedParentNames returns the selected parent names for assetType, plus
// true when ParentScope restricts this asset type. When the asset type is
// NOT in the scope it returns (nil, false) so the caller falls back to its
// project-wide enumeration. A nil/empty ParentScope always returns
// (nil, false).
//
// CRITICAL (mirrors the AWS (empty, true) contract): NewParentScope never
// stores an empty slice for a present key — an asset type that survived
// construction has at least one usable parent. The (empty, true) "scoped
// but no parent, skip enumeration" case is therefore expressed by the
// asset type's ABSENCE from the scope combined with the caller's
// knowledge that the discoverer is a registered child whose closure
// requested it: DiscoverTypes routes such a type into the skipped set (see
// partitionScopedAssetTypes). scopedParentNames itself only answers "is
// this asset type scoped, and to which names".
func (a DiscoverArgs) scopedParentNames(assetType string) ([]string, bool) {
	if len(a.ParentScope) == 0 {
		return nil, false
	}
	parents, ok := a.ParentScope[strings.TrimSpace(assetType)]
	if !ok || len(parents) == 0 {
		return nil, false
	}
	out := make([]string, 0, len(parents))
	for _, p := range parents {
		out = append(out, p.Name)
	}
	return out, true
}

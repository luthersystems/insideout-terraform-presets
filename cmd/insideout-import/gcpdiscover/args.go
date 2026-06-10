package gcpdiscover

import (
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

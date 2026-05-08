package gcpdiscover

import "github.com/luthersystems/insideout-terraform-presets/cmd/insideout-import/progress"

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

package imported

import (
	"sort"
	"time"
)

// DiscoverySummary is the on-the-wire shape persisted next to imported.json
// (and unsupported.json + graph.json) by `insideout-import discover`. The
// reliable importer wizard's discovery-review screen reads it directly,
// rather than computing the per-bucket counts client-side over a potentially
// large imported.json. Always emitted (no flag); see writeSummary in
// cmd/insideout-import/manifest.go for the persistence half.
//
// Wire-format invariants:
//
//   - Total / Importable / Unsupported are always present (omitempty is NOT
//     set on these three; an absent zero is wire-distinguishable from the
//     "no resources discovered" case).
//   - All four map fields default to map[string]int{} (NOT nil) so the
//     persisted summary.json marshals empty buckets as `{}` not `null`.
//     The discovery-review screen iterates the maps unconditionally and
//     a `null` body would crash the iterator.
//   - ScanSummary.RegionsScanned and TagSelectors default to []string{}
//     (NOT nil) for the same reason.
//   - Map keys are emitted by Go's encoding/json in sorted order, so
//     summary.json is byte-deterministic across runs of the same input.
//
// See issue #298 (gap #8 of #289).
type DiscoverySummary struct {
	// Total is the count of resources visible to the discover run —
	// importable + unsupported. When --include-unsupported is unset,
	// Total == Importable.
	Total int `json:"total"`
	// Importable is the count of rows in the companion imported.json
	// (i.e. resources the discover pipeline can drive end-to-end).
	Importable int `json:"importable"`
	// Unsupported is the count of rows in the companion unsupported.json,
	// 0 when --include-unsupported was not set on the run.
	Unsupported int `json:"unsupported"`
	// ByType buckets the importable set by Identity.Type (e.g.
	// "aws_sqs_queue" → 5). Empty input → {}.
	ByType map[string]int `json:"by_type"`
	// ByRegion buckets the importable set by Identity.Region. Resources
	// with an empty Region land in the "" bucket so the count totals
	// match Importable.
	ByRegion map[string]int `json:"by_region"`
	// ByTag buckets the importable set by `<key>=<value>` strings drawn
	// from Identity.Tags. A single resource with two tag pairs
	// contributes to two ByTag entries. Resources with nil Tags
	// contribute nothing — there is no synthetic empty entry.
	ByTag map[string]int `json:"by_tag"`
	// ByGroup buckets the importable set by the high-level UI category
	// returned by Category(Identity.Type). Types with no Category mapping
	// land in the "" bucket so the picker can show them under "Other".
	// Useful for the discovery-review screen's group toggles.
	ByGroup map[string]int `json:"by_group"`
	// ScanSummary records the scope and wall-time of the discover run.
	ScanSummary ScanSummary `json:"scan_summary"`
}

// ScanSummary captures the operator-supplied scope (regions + tag
// selectors) and the observed wall-time of the discover stage.
//
// Cloud is always populated to "aws" or "gcp"; the omitempty avoids
// emitting an empty string when the caller misuses SummarizeResources
// with an empty SummaryOpts.Cloud (the test suite pins the expected
// non-empty value).
type ScanSummary struct {
	// DurationMs is the total wall-time of the discover run in
	// milliseconds. Zero when the caller did not supply a duration.
	DurationMs int64 `json:"duration_ms,omitempty"`
	// RegionsScanned is the resolved --regions list (after deprecated
	// --region alias resolution). Defaulted to []string{} so the field
	// always serializes as `[]` rather than `null`.
	RegionsScanned []string `json:"regions_scanned"`
	// TagSelectors is the parsed --tag-selectors list, in `key=value`
	// string form. Defaulted to []string{} for the same reason.
	TagSelectors []string `json:"tag_selectors"`
	// Cloud is "aws" or "gcp". omitempty covers misuse but the discover
	// wiring always supplies a value.
	Cloud string `json:"cloud,omitempty"`
}

// SummaryTagSelector is a single parsed tag-selector pair. Declared in
// this package so callers (cmd/insideout-import) can convert their
// CLI-level tagSelectorPair into something SummarizeResources accepts
// without coupling pkg/composer/imported to the CLI package.
type SummaryTagSelector struct {
	Key   string
	Value string
}

// SummaryOpts is the non-resource scope SummarizeResources needs to
// populate ScanSummary and the Total / Unsupported counts. The struct
// is value-typed (not a pointer) to keep the call site obvious — there
// is no "no opts" mode, every field is explicitly set by the caller.
type SummaryOpts struct {
	// Cloud is "aws" or "gcp"; threaded into ScanSummary.Cloud.
	Cloud string
	// UnsupportedCount is the row count from unsupported.json when
	// --include-unsupported was set; 0 otherwise. Total =
	// len(resources) + UnsupportedCount.
	UnsupportedCount int
	// Duration is the wall-time of the discover stage. Zero is permitted
	// and serializes as omitempty.
	Duration time.Duration
	// Regions is the resolved --regions list.
	Regions []string
	// TagSelectors is the parsed --tag-selectors list. Cloud-agnostic
	// shape; the CLI's tagSelectorPair maps trivially onto this.
	TagSelectors []SummaryTagSelector
}

// SummarizeResources computes the DiscoverySummary aggregate from a
// resource set + scope opts. Pure function: no globals, no I/O, no
// time.Now (the caller passes Duration). Determinism comes from the
// caller-supplied input order being irrelevant — every output bucket
// is a map (Go's encoding/json marshals sorted) or an explicitly
// sorted slice.
//
// Empty input is valid and produces a summary with Total=0, all maps
// non-nil-empty, RegionsScanned and TagSelectors echoed back from
// opts (still defaulted to []string{} when the caller passes nil).
//
// Region-vs-Location: ScanSummary tracks Regions but ByRegion buckets
// by Identity.Region. When a GCP resource carries both Region and
// Location populated, Region wins (per the SummaryOpts/SummaryOpts
// design contract — the discovery-review screen surfaces Region
// uniformly across both clouds).
func SummarizeResources(resources []ImportedResource, opts SummaryOpts) DiscoverySummary {
	out := DiscoverySummary{
		Importable:  len(resources),
		Unsupported: opts.UnsupportedCount,
		Total:       len(resources) + opts.UnsupportedCount,
		ByType:      map[string]int{},
		ByRegion:    map[string]int{},
		ByTag:       map[string]int{},
		ByGroup:     map[string]int{},
		ScanSummary: ScanSummary{
			Cloud:          opts.Cloud,
			DurationMs:     opts.Duration.Milliseconds(),
			RegionsScanned: copyStrings(opts.Regions),
			TagSelectors:   tagSelectorStrings(opts.TagSelectors),
		},
	}

	for _, r := range resources {
		out.ByType[r.Identity.Type]++
		out.ByRegion[r.Identity.Region]++
		out.ByGroup[Category(r.Identity.Type)]++

		// Identity.Tags is intentionally allowed to be nil (per #291,
		// nil distinguishes "discoverer didn't fetch tags" from an
		// empty map). Nil contributes nothing to ByTag.
		for k, v := range r.Identity.Tags {
			out.ByTag[k+"="+v]++
		}
	}

	return out
}

// copyStrings returns a non-nil clone of s. Used to defend ScanSummary
// from upstream mutation and to coerce nil inputs into []string{} so
// the JSON output is always `[]` (never `null`).
func copyStrings(s []string) []string {
	out := make([]string, len(s))
	copy(out, s)
	return out
}

// tagSelectorStrings flattens []SummaryTagSelector into the canonical
// `key=value` string slice. Keeps the wire shape stable across cloud
// providers — the AWS adapter's tagSelectorPair and GCP adapter's
// tagSelectorPair both carry Key/Value; either funnels through
// SummaryTagSelector here.
//
// The output is sorted to match Go's map-marshal sort order on ByTag,
// so a consumer that diffs `tag_selectors` against the keys of
// `by_tag` sees stable ordering across runs.
func tagSelectorStrings(sels []SummaryTagSelector) []string {
	out := make([]string, 0, len(sels))
	for _, s := range sels {
		out = append(out, s.Key+"="+s.Value)
	}
	sort.Strings(out)
	return out
}

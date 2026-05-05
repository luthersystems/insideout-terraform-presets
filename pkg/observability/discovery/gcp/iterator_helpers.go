// Iterator drain helpers — generalize the Firestore collectXIDs
// pattern (#255 / #256) across all GCP gRPC inspectors.
//
// Every list-call site in this package follows the same shape:
//
//	out := []T{}
//	for {
//	    v, err := it.Next()
//	    if err == iterator.Done { break }
//	    if err != nil { return nil, err }
//	    if !keep(v) { continue }
//	    out = append(out, v)
//	}
//	return out, nil
//
// drainIterator and drainAggregatedIterator extract that shape so each
// site is one line + a closure, and unit tests can pin the empty-state
// JSON-shape contract through a single fake iterator implementing the
// minimal `Next()` method.
//
// IMPORTANT: the empty path MUST return []T{} (a non-nil slice), NOT
// `nil`. encoding/json marshals a nil slice as JSON `null`, which the
// reliable UI's panel renderer collapses through every empty-state
// branch onto the misleading "Deploy infrastructure first." fallback
// even when the resource is healthy and just has zero items (#255).
//
// Test contract: `require.NotNil` + `json.Marshal == "[]"`. See
// pkg/observability/discovery/CONTRIBUTING.md.

package gcp

import "google.golang.org/api/iterator"

// gcpIterator is the minimal slice of every typed gapic iterator
// (*pubsubpb.TopicIterator, *runpb.ServiceIterator, etc.) that the
// drain helpers consume. The concrete iterator types satisfy this
// interface structurally — no adapter required.
type gcpIterator[T any] interface {
	Next() (T, error)
}

// drainIterator drains a typed gapic iterator into a non-nil []T. The
// empty-iterator path returns []T{}, NOT nil — pinned by the per-site
// _NoX_EmptySlice tests so downstream JSON marshals as `[]`, not
// `null` (#255 contract).
//
// keep is an optional post-filter; pass nil to accept everything.
func drainIterator[T any](it gcpIterator[T], keep func(T) bool) ([]T, error) {
	out := []T{}
	for {
		v, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		if keep != nil && !keep(v) {
			continue
		}
		out = append(out, v)
	}
	return out, nil
}

// drainIteratorBounded is drainIterator with a max-items cap. After the
// cap the helper peeks once more to detect truncation; pagination reads
// a full page ahead so the peek doesn't fetch another page. Used by
// inspectCloudBuild's list-builds, which caps at the most-recent N
// builds (newest-first per the ListBuilds API).
func drainIteratorBounded[T any](it gcpIterator[T], maxN int) (out []T, truncated bool, err error) {
	out = []T{}
	for len(out) < maxN {
		v, e := it.Next()
		if e == iterator.Done {
			return out, false, nil
		}
		if e != nil {
			return nil, false, e
		}
		out = append(out, v)
	}
	if _, peekErr := it.Next(); peekErr != iterator.Done {
		truncated = true
	}
	return out, truncated, nil
}

// drainAggregatedIterator flattens a Compute AggregatedList iterator —
// which yields zone-keyed pairs whose `pair.Value.<sub>` carries an
// inner slice — into a flat []T. extract pulls the inner slice out of
// each pair (callers handle nil pairs / nil inner slices by returning
// nil/empty from extract). keep is the per-element post-filter.
func drainAggregatedIterator[Pair, T any](
	it gcpIterator[Pair],
	extract func(Pair) []T,
	keep func(T) bool,
) ([]T, error) {
	out := []T{}
	for {
		p, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		for _, v := range extract(p) {
			if keep != nil && !keep(v) {
				continue
			}
			out = append(out, v)
		}
	}
	return out, nil
}

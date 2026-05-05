// Tests for the iterator drain helpers used by every GCP gRPC
// inspector. The empty-state contract pinned here applies transitively
// to every call site that consumes drainIterator /
// drainIteratorBounded / drainAggregatedIterator (#256).
//
// The mutation-resistant pin is the standard #255 / #256 trio:
//
//	require.NotNil(t, got)
//	json.Marshal(got) == "[]"
//
// `assert.Empty` alone is INSUFFICIENT — it accepts both nil and
// empty (per pkg/observability/discovery/CONTRIBUTING.md:114-121).

package gcp

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/iterator"
)

// emptyIterator yields iterator.Done on the first Next() call —
// the canonical empty-state input shape for the helpers.
type emptyIterator[T any] struct{}

func (*emptyIterator[T]) Next() (T, error) {
	var zero T
	return zero, iterator.Done
}

// fixedIterator yields the items in `items` then iterator.Done. If
// err is non-nil, returns it on the first call (mirrors the pre-PR-257
// fakeFirestoreIterator's short-circuit behavior).
type fixedIterator[T any] struct {
	items []T
	idx   int
	err   error
}

func (f *fixedIterator[T]) Next() (T, error) {
	var zero T
	if f.err != nil {
		return zero, f.err
	}
	if f.idx >= len(f.items) {
		return zero, iterator.Done
	}
	v := f.items[f.idx]
	f.idx++
	return v, nil
}

func TestDrainIterator_Empty_EmptySlice(t *testing.T) {
	t.Parallel()
	got, err := drainIterator(&emptyIterator[*int]{}, nil)
	require.NoError(t, err)
	require.NotNil(t, got, "must be non-nil so encoding/json emits [] not null")
	assert.Empty(t, got)

	b, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b),
		"empty drainIterator must marshal as [] not null (#256)")
}

func TestDrainIterator_AllItemsPass(t *testing.T) {
	t.Parallel()
	got, err := drainIterator(&fixedIterator[int]{items: []int{1, 2, 3}}, nil)
	require.NoError(t, err)
	assert.Equal(t, []int{1, 2, 3}, got)
}

func TestDrainIterator_KeepFiltersItems(t *testing.T) {
	t.Parallel()
	got, err := drainIterator(
		&fixedIterator[int]{items: []int{1, 2, 3, 4, 5}},
		func(v int) bool { return v%2 == 0 },
	)
	require.NoError(t, err)
	assert.Equal(t, []int{2, 4}, got)
}

func TestDrainIterator_AllFiltered_EmptySlice(t *testing.T) {
	t.Parallel()
	// All items rejected by predicate → empty result must still
	// marshal as `[]`, NOT `null`. This is the most-likely production
	// failure mode (#256): a project filter that matches nothing
	// against a populated upstream — pre-fix, the inspector returned
	// nil and reliable's panel collapsed onto the misleading "Deploy
	// infrastructure first." fallback.
	got, err := drainIterator(
		&fixedIterator[int]{items: []int{1, 2, 3}},
		func(int) bool { return false },
	)
	require.NoError(t, err)
	require.NotNil(t, got)
	b, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b),
		"all-filtered drainIterator must marshal as [] not null (#256)")
}

func TestDrainIterator_PropagatesError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("boom")
	_, err := drainIterator(&fixedIterator[int]{err: sentinel}, nil)
	assert.ErrorIs(t, err, sentinel)
}

func TestDrainIteratorBounded_Empty_EmptySlice(t *testing.T) {
	t.Parallel()
	got, truncated, err := drainIteratorBounded(&emptyIterator[*int]{}, 100)
	require.NoError(t, err)
	require.NotNil(t, got, "must be non-nil so encoding/json emits [] not null")
	assert.False(t, truncated, "empty iterator must NOT report truncated")

	b, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b),
		"empty drainIteratorBounded must marshal as [] not null (#256)")
}

func TestDrainIteratorBounded_UnderCap_NotTruncated(t *testing.T) {
	t.Parallel()
	got, truncated, err := drainIteratorBounded(
		&fixedIterator[int]{items: []int{1, 2, 3}},
		100,
	)
	require.NoError(t, err)
	assert.Equal(t, []int{1, 2, 3}, got)
	assert.False(t, truncated)
}

func TestDrainIteratorBounded_AtCap_NoMoreItems_NotTruncated(t *testing.T) {
	t.Parallel()
	// Exactly at the cap with no further items — the peek-Next() must
	// return iterator.Done and truncated must remain false. Pinning this
	// distinguishes "exactly N items" from "we capped".
	got, truncated, err := drainIteratorBounded(
		&fixedIterator[int]{items: []int{1, 2, 3}},
		3,
	)
	require.NoError(t, err)
	assert.Equal(t, []int{1, 2, 3}, got)
	assert.False(t, truncated, "exactly cap with no further items must NOT be truncated")
}

func TestDrainIteratorBounded_AtCap_MoreItems_IsTruncated(t *testing.T) {
	t.Parallel()
	got, truncated, err := drainIteratorBounded(
		&fixedIterator[int]{items: []int{1, 2, 3, 4, 5}},
		3,
	)
	require.NoError(t, err)
	assert.Equal(t, []int{1, 2, 3}, got)
	assert.True(t, truncated, "more items beyond cap must report truncated")
}

// pair mirrors compute.InstancesScopedListPair's structural shape
// — Value field carrying an inner slice — for testing
// drainAggregatedIterator without depending on the compute SDK's
// concrete pair type.
type pair[T any] struct {
	Value *[]T
}

func TestDrainAggregatedIterator_Empty_EmptySlice(t *testing.T) {
	t.Parallel()
	got, err := drainAggregatedIterator(
		&emptyIterator[pair[int]]{},
		func(p pair[int]) []int {
			if p.Value == nil {
				return nil
			}
			return *p.Value
		},
		nil,
	)
	require.NoError(t, err)
	require.NotNil(t, got, "must be non-nil so encoding/json emits [] not null")
	assert.Empty(t, got)

	b, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b),
		"empty drainAggregatedIterator must marshal as [] not null (#256)")
}

func TestDrainAggregatedIterator_AllPairsEmpty_EmptySlice(t *testing.T) {
	t.Parallel()
	// Iterator yields pairs but each pair's inner slice is empty/nil
	// — the post-flatmap result must still be []T{}, NOT nil.
	got, err := drainAggregatedIterator(
		&fixedIterator[pair[int]]{items: []pair[int]{
			{Value: nil},
			{Value: &[]int{}},
		}},
		func(p pair[int]) []int {
			if p.Value == nil {
				return nil
			}
			return *p.Value
		},
		nil,
	)
	require.NoError(t, err)
	require.NotNil(t, got)
	b, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b),
		"all-pairs-empty drainAggregatedIterator must marshal as [] not null (#256)")
}

func TestDrainAggregatedIterator_FlattensInnerSlices(t *testing.T) {
	t.Parallel()
	a := []int{1, 2}
	b := []int{3, 4, 5}
	got, err := drainAggregatedIterator(
		&fixedIterator[pair[int]]{items: []pair[int]{
			{Value: &a},
			{Value: &b},
		}},
		func(p pair[int]) []int {
			if p.Value == nil {
				return nil
			}
			return *p.Value
		},
		nil,
	)
	require.NoError(t, err)
	assert.Equal(t, []int{1, 2, 3, 4, 5}, got)
}

func TestDrainAggregatedIterator_KeepFiltersInner(t *testing.T) {
	t.Parallel()
	a := []int{1, 2, 3}
	b := []int{4, 5, 6}
	got, err := drainAggregatedIterator(
		&fixedIterator[pair[int]]{items: []pair[int]{
			{Value: &a},
			{Value: &b},
		}},
		func(p pair[int]) []int {
			if p.Value == nil {
				return nil
			}
			return *p.Value
		},
		func(v int) bool { return v%2 == 0 },
	)
	require.NoError(t, err)
	assert.Equal(t, []int{2, 4, 6}, got)
}

func TestDrainAggregatedIterator_AllInnerFiltered_EmptySlice(t *testing.T) {
	t.Parallel()
	a := []int{1, 3, 5}
	got, err := drainAggregatedIterator(
		&fixedIterator[pair[int]]{items: []pair[int]{{Value: &a}}},
		func(p pair[int]) []int {
			if p.Value == nil {
				return nil
			}
			return *p.Value
		},
		func(int) bool { return false },
	)
	require.NoError(t, err)
	require.NotNil(t, got)
	b, err := json.Marshal(got)
	require.NoError(t, err)
	assert.Equal(t, "[]", string(b),
		"all-inner-filtered drainAggregatedIterator must marshal as [] not null (#256)")
}

func TestDrainAggregatedIterator_PropagatesError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("boom")
	_, err := drainAggregatedIterator(
		&fixedIterator[pair[int]]{err: sentinel},
		func(pair[int]) []int { return nil },
		nil,
	)
	assert.ErrorIs(t, err, sentinel)
}

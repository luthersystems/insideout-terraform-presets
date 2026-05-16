package gcpdiscover

// cai_normalizers_test.go — table-driven unit tests for the composable
// CAI Normalizer helpers (#510). Mirrors the AWS Cloud Control test
// shape in cloudcontrol_normalizers_test.go: each helper gets a small
// table covering its happy path, idempotent pass-through paths, and
// the malformed-shape error paths. Pulling the helpers in via the
// production registration would make these end-to-end tests; the unit
// surface here proves the building blocks themselves stay
// well-behaved.

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestChain_EmptyAndNilEntriesPassThrough pins the no-op contract: a
// chain of zero entries, or one whose entries are all nil, returns
// the input unchanged. Callers that build conditional chains rely on
// this so they don't have to branch on "did anything register".
func TestChain_EmptyAndNilEntriesPassThrough(t *testing.T) {
	t.Parallel()
	in := json.RawMessage(`{"a":1}`)

	t.Run("empty list", func(t *testing.T) {
		t.Parallel()
		n := chain()
		out, err := n(in)
		require.NoError(t, err)
		assert.JSONEq(t, string(in), string(out))
	})

	t.Run("all nil entries", func(t *testing.T) {
		t.Parallel()
		n := chain(nil, nil, nil)
		out, err := n(in)
		require.NoError(t, err)
		assert.JSONEq(t, string(in), string(out))
	})
}

// TestChain_AppliesInOrder pins the registration-order contract: two
// helpers that touch the same key apply in the order chain sees them.
// A regression that flipped iteration direction would silently break
// any caller relying on the rename-then-flatten ordering used by the
// instance / firewall configs.
func TestChain_AppliesInOrder(t *testing.T) {
	t.Parallel()
	first := func(in json.RawMessage) (json.RawMessage, error) {
		var m map[string]any
		require.NoError(t, json.Unmarshal(in, &m))
		m["seen"] = []any{"first"}
		return json.Marshal(m)
	}
	second := func(in json.RawMessage) (json.RawMessage, error) {
		var m map[string]any
		require.NoError(t, json.Unmarshal(in, &m))
		seen, _ := m["seen"].([]any)
		seen = append(seen, "second")
		m["seen"] = seen
		return json.Marshal(m)
	}
	n := chain(first, second)
	out, err := n(json.RawMessage(`{}`))
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(out, &got))
	assert.Equal(t, []any{"first", "second"}, got["seen"])
}

// TestChain_ShortCircuitsOnError pins the failure-path contract: the
// first step to return a non-nil error short-circuits the chain and
// the per-step index appears in the wrapped message so the operator
// can attribute the failure.
func TestChain_ShortCircuitsOnError(t *testing.T) {
	t.Parallel()
	boom := errors.New("kaboom")
	first := func(in json.RawMessage) (json.RawMessage, error) { return in, nil }
	second := func(_ json.RawMessage) (json.RawMessage, error) { return nil, boom }
	third := func(_ json.RawMessage) (json.RawMessage, error) {
		t.Fatal("third step ran after second returned error")
		return nil, nil
	}
	n := chain(first, second, third)
	_, err := n(json.RawMessage(`{}`))
	require.Error(t, err)
	assert.ErrorIs(t, err, boom)
	assert.Contains(t, err.Error(), "step 1")
}

// TestSelfLinkToBareName_TableDriven pins the happy paths and the
// idempotent pass-through paths for the most common helper in the
// CAI Normalizer kit.
func TestSelfLinkToBareName_TableDriven(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		key  string
		in   string
		want string
	}{
		{
			name: "full https self-link collapses to short name",
			key:  "network",
			in:   `{"network":"https://www.googleapis.com/compute/v1/projects/x/global/networks/foo"}`,
			want: `{"network":"foo"}`,
		},
		{
			name: "projects-rooted self-link collapses to short name",
			key:  "machineType",
			in:   `{"machineType":"projects/x/zones/us-east1-b/machineTypes/n1-standard-1"}`,
			want: `{"machineType":"n1-standard-1"}`,
		},
		{
			name: "bare name passes through unchanged",
			key:  "network",
			in:   `{"network":"foo"}`,
			want: `{"network":"foo"}`,
		},
		{
			name: "missing key passes through unchanged",
			key:  "network",
			in:   `{"description":"x"}`,
			want: `{"description":"x"}`,
		},
		{
			name: "non-string value passes through unchanged",
			key:  "network",
			in:   `{"network":42}`,
			want: `{"network":42}`,
		},
		{
			name: "null value passes through unchanged",
			key:  "network",
			in:   `{"network":null}`,
			want: `{"network":null}`,
		},
		{
			name: "empty key parameter is a no-op",
			key:  "",
			in:   `{"network":"https://x/y/z"}`,
			want: `{"network":"https://x/y/z"}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			n := selfLinkToBareName(tc.key)
			got, err := n(json.RawMessage(tc.in))
			require.NoError(t, err)
			assert.JSONEq(t, tc.want, string(got))
		})
	}
}

// TestSelfLinkSliceToBareNames_TableDriven pins the list-form
// counterpart used by fields like resource_policies whose CAI shape
// is a list-of-self-links and whose TF shape is a list-of-bare-names.
func TestSelfLinkSliceToBareNames_TableDriven(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		key  string
		in   string
		want string
	}{
		{
			name: "list of self-links collapses to short names",
			key:  "resourcePolicies",
			in:   `{"resourcePolicies":["projects/x/regions/r/resourcePolicies/p1","https://www.googleapis.com/compute/v1/projects/x/regions/r/resourcePolicies/p2"]}`,
			want: `{"resourcePolicies":["p1","p2"]}`,
		},
		{
			name: "list of bare names passes through unchanged",
			key:  "resourcePolicies",
			in:   `{"resourcePolicies":["p1","p2"]}`,
			want: `{"resourcePolicies":["p1","p2"]}`,
		},
		{
			name: "missing key passes through unchanged",
			key:  "resourcePolicies",
			in:   `{"other":1}`,
			want: `{"other":1}`,
		},
		{
			name: "non-list value passes through unchanged",
			key:  "resourcePolicies",
			in:   `{"resourcePolicies":"x"}`,
			want: `{"resourcePolicies":"x"}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			n := selfLinkSliceToBareNames(tc.key)
			got, err := n(json.RawMessage(tc.in))
			require.NoError(t, err)
			assert.JSONEq(t, tc.want, string(got))
		})
	}
}

// TestFlattenNetworkTags_TableDriven pins every shape the helper
// must accept (wrapped, already-flat, empty wrapper, absent) plus
// the malformed-shape error path so a regression that loosened the
// type check would surface here.
func TestFlattenNetworkTags_TableDriven(t *testing.T) {
	t.Parallel()
	t.Run("wrapped tags unwrap to bare list", func(t *testing.T) {
		t.Parallel()
		n := flattenNetworkTags()
		got, err := n(json.RawMessage(`{"tags":{"items":["web","ssh"],"fingerprint":"abc"}}`))
		require.NoError(t, err)
		assert.JSONEq(t, `{"tags":["web","ssh"]}`, string(got))
	})

	t.Run("already-flat list passes through unchanged", func(t *testing.T) {
		t.Parallel()
		n := flattenNetworkTags()
		got, err := n(json.RawMessage(`{"tags":["web","ssh"]}`))
		require.NoError(t, err)
		assert.JSONEq(t, `{"tags":["web","ssh"]}`, string(got))
	})

	t.Run("wrapper missing items drops tags entirely", func(t *testing.T) {
		t.Parallel()
		n := flattenNetworkTags()
		got, err := n(json.RawMessage(`{"tags":{"fingerprint":"abc"},"name":"x"}`))
		require.NoError(t, err)
		assert.JSONEq(t, `{"name":"x"}`, string(got))
	})

	t.Run("missing tags key passes through unchanged", func(t *testing.T) {
		t.Parallel()
		n := flattenNetworkTags()
		got, err := n(json.RawMessage(`{"name":"x"}`))
		require.NoError(t, err)
		assert.JSONEq(t, `{"name":"x"}`, string(got))
	})

	t.Run("malformed tags shape errors", func(t *testing.T) {
		t.Parallel()
		n := flattenNetworkTags()
		_, err := n(json.RawMessage(`{"tags":"not-an-object-or-list"}`))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unexpected shape")
	})

	t.Run("malformed tags.items shape errors", func(t *testing.T) {
		t.Parallel()
		n := flattenNetworkTags()
		_, err := n(json.RawMessage(`{"tags":{"items":"not-a-list"}}`))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "items has unexpected shape")
	})
}

// TestDecodeCAIObject_PassThroughCases pins the no-op contracts the
// helpers depend on — empty input / "null" / whitespace all collapse
// to (nil, nil) so the helpers can short-circuit without re-checking
// at every call site.
func TestDecodeCAIObject_PassThroughCases(t *testing.T) {
	t.Parallel()
	cases := []json.RawMessage{
		json.RawMessage(``),
		json.RawMessage(`null`),
		json.RawMessage(`  `),
	}
	for _, c := range cases {
		c := c
		t.Run(string(c), func(t *testing.T) {
			t.Parallel()
			m, err := decodeCAIObject(c)
			require.NoError(t, err)
			assert.Nil(t, m)
		})
	}
}

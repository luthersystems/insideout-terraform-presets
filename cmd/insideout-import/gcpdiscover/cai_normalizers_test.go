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

// TestDropLabelPrefix_TableDriven pins the goog-managed-label filter
// that #511 hand-rolled enrichers all open-code as their only post-
// mapping cleanup pass. Covers happy path, both prefixes (`goog-` and
// `goog_`), the all-filtered-collapses-to-nil branch (must drop the
// labels field entirely so the emit layer omits the attribute), and
// the absent / non-map shape pass-through paths.
func TestDropLabelPrefix_TableDriven(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		field  string
		prefix string
		in     string
		want   string
	}{
		{
			name:   "filters goog-managed labels keeps user labels",
			field:  "labels",
			prefix: "goog-",
			in:     `{"labels":{"team":"platform","env":"prod","goog-managed":"true"}}`,
			want:   `{"labels":{"team":"platform","env":"prod"}}`,
		},
		{
			name:   "all-goog map collapses to absent labels field",
			field:  "labels",
			prefix: "goog-",
			in:     `{"labels":{"goog-managed":"true","goog-other":"1"},"name":"x"}`,
			want:   `{"name":"x"}`,
		},
		{
			name:   "no matching prefix is a no-op",
			field:  "labels",
			prefix: "goog-",
			in:     `{"labels":{"team":"platform"}}`,
			want:   `{"labels":{"team":"platform"}}`,
		},
		{
			name:   "missing labels field passes through",
			field:  "labels",
			prefix: "goog-",
			in:     `{"name":"x"}`,
			want:   `{"name":"x"}`,
		},
		{
			name:   "null labels passes through",
			field:  "labels",
			prefix: "goog-",
			in:     `{"labels":null}`,
			want:   `{"labels":null}`,
		},
		{
			name:   "non-map labels value passes through unchanged",
			field:  "labels",
			prefix: "goog-",
			in:     `{"labels":"not-a-map"}`,
			want:   `{"labels":"not-a-map"}`,
		},
		{
			name:   "empty field arg is a no-op",
			field:  "",
			prefix: "goog-",
			in:     `{"labels":{"goog-x":"y"}}`,
			want:   `{"labels":{"goog-x":"y"}}`,
		},
		{
			name:   "empty prefix arg is a no-op",
			field:  "labels",
			prefix: "",
			in:     `{"labels":{"goog-x":"y"}}`,
			want:   `{"labels":{"goog-x":"y"}}`,
		},
		{
			name:   "underscore prefix variant",
			field:  "labels",
			prefix: "goog_",
			in:     `{"labels":{"goog_internal":"ignore","team":"x"}}`,
			want:   `{"labels":{"team":"x"}}`,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			n := dropLabelPrefix(tc.field, tc.prefix)
			got, err := n(json.RawMessage(tc.in))
			require.NoError(t, err)
			assert.JSONEq(t, tc.want, string(got))
		})
	}
}

// TestDropLabelPrefix_ComposableForGoogDashAndUnderscore pins the
// canonical use site: a chain of two dropLabelPrefix calls collapses
// both `goog-` and `goog_` keys in a single normalizer pass. The
// hand-rolled enrichers (compute_address_enrich.go,
// pubsub_topic_enrich.gen.go, storage_bucket_enrich.gen.go) all check
// BOTH prefixes; the CAI-side replacement is two helpers chained.
func TestDropLabelPrefix_ComposableForGoogDashAndUnderscore(t *testing.T) {
	t.Parallel()
	n := chain(
		dropLabelPrefix("labels", "goog-"),
		dropLabelPrefix("labels", "goog_"),
	)
	in := json.RawMessage(`{"labels":{"goog-managed":"true","goog_internal":"x","team":"platform"}}`)
	got, err := n(in)
	require.NoError(t, err)
	assert.JSONEq(t, `{"labels":{"team":"platform"}}`, string(got))
}

// TestShortenLastSegment_TableDriven pins the resource-name shortener
// used to convert `projects/<p>/topics/<n>` → `<n>` for Pub/Sub,
// Secret Manager, and similar fully-qualified-name fields. Covers
// happy path, idempotent pass-through, and the no-slash, missing,
// non-string edge cases.
func TestShortenLastSegment_TableDriven(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		field string
		in    string
		want  string
	}{
		{
			name:  "projects-rooted topic name shortens",
			field: "name",
			in:    `{"name":"projects/my-proj/topics/my-topic"}`,
			want:  `{"name":"my-topic"}`,
		},
		{
			name:  "projects-rooted subscription name shortens",
			field: "name",
			in:    `{"name":"projects/my-proj/subscriptions/my-sub"}`,
			want:  `{"name":"my-sub"}`,
		},
		{
			name:  "bare short name passes through",
			field: "name",
			in:    `{"name":"my-topic"}`,
			want:  `{"name":"my-topic"}`,
		},
		{
			name:  "missing field passes through",
			field: "name",
			in:    `{"other":"x"}`,
			want:  `{"other":"x"}`,
		},
		{
			name:  "non-string value passes through",
			field: "name",
			in:    `{"name":42}`,
			want:  `{"name":42}`,
		},
		{
			name:  "null value passes through",
			field: "name",
			in:    `{"name":null}`,
			want:  `{"name":null}`,
		},
		{
			name:  "empty field arg is a no-op",
			field: "",
			in:    `{"name":"projects/p/topics/t"}`,
			want:  `{"name":"projects/p/topics/t"}`,
		},
		{
			name:  "empty string value stays empty",
			field: "name",
			in:    `{"name":""}`,
			want:  `{"name":""}`,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			n := shortenLastSegment(tc.field)
			got, err := n(json.RawMessage(tc.in))
			require.NoError(t, err)
			assert.JSONEq(t, tc.want, string(got))
		})
	}
}

// TestSetDefaultIfAbsent_TableDriven pins the default-injection
// helper used to emit TF-required defaults the CAI body omits. The
// canonical use is `force_destroy = false` on google_storage_bucket.
// Covers happy path (field missing), no-op when field already
// present (even if null), and the empty-input synthesis.
func TestSetDefaultIfAbsent_TableDriven(t *testing.T) {
	t.Parallel()

	t.Run("missing field gets default", func(t *testing.T) {
		t.Parallel()
		n := setDefaultIfAbsent("force_destroy", false)
		got, err := n(json.RawMessage(`{"name":"my-bucket"}`))
		require.NoError(t, err)
		assert.JSONEq(t, `{"name":"my-bucket","force_destroy":false}`, string(got))
	})

	t.Run("present field is not overwritten", func(t *testing.T) {
		t.Parallel()
		n := setDefaultIfAbsent("force_destroy", false)
		got, err := n(json.RawMessage(`{"force_destroy":true,"name":"my-bucket"}`))
		require.NoError(t, err)
		assert.JSONEq(t, `{"force_destroy":true,"name":"my-bucket"}`, string(got))
	})

	t.Run("present-with-null field is not overwritten", func(t *testing.T) {
		t.Parallel()
		n := setDefaultIfAbsent("force_destroy", false)
		got, err := n(json.RawMessage(`{"force_destroy":null,"name":"my-bucket"}`))
		require.NoError(t, err)
		// Null preserved; the caller wanted an explicit null and the
		// helper must not silently overwrite that.
		assert.JSONEq(t, `{"force_destroy":null,"name":"my-bucket"}`, string(got))
	})

	t.Run("string default", func(t *testing.T) {
		t.Parallel()
		n := setDefaultIfAbsent("storage_class", "STANDARD")
		got, err := n(json.RawMessage(`{"name":"b"}`))
		require.NoError(t, err)
		assert.JSONEq(t, `{"name":"b","storage_class":"STANDARD"}`, string(got))
	})

	t.Run("empty input synthesizes minimal object", func(t *testing.T) {
		t.Parallel()
		n := setDefaultIfAbsent("force_destroy", false)
		got, err := n(json.RawMessage(``))
		require.NoError(t, err)
		assert.JSONEq(t, `{"force_destroy":false}`, string(got))
	})

	t.Run("null input synthesizes minimal object", func(t *testing.T) {
		t.Parallel()
		n := setDefaultIfAbsent("force_destroy", false)
		got, err := n(json.RawMessage(`null`))
		require.NoError(t, err)
		assert.JSONEq(t, `{"force_destroy":false}`, string(got))
	})

	t.Run("empty field arg is a no-op", func(t *testing.T) {
		t.Parallel()
		n := setDefaultIfAbsent("", false)
		got, err := n(json.RawMessage(`{"name":"x"}`))
		require.NoError(t, err)
		assert.JSONEq(t, `{"name":"x"}`, string(got))
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

// TestStripComputedOnlyForType_TableDriven exercises the #581 generic
// computed-only filter against the real generated.GoogleComputeAddress
// schema. compute_address is chosen as the representative type because
// its schema covers all four schema-flag combinations the helper must
// distinguish: required-only (name), optional-only (description),
// optional+computed (address, network_tier — user-overridable),
// computed-only (creation_timestamp, effective_labels, label_fingerprint,
// self_link, terraform_labels, users). Combined with `id`
// (Optional+Computed but on the universal-elide allowlist), this single
// type tables out every branch in stripComputedOnlyForType.
//
// Tests against the LIVE schema registration (not a fixture) so a
// future regeneration of google_compute_address.gen.go that flips a
// flag from Computed-only to Optional+Computed fails this test
// immediately — guarding against silent parity drift.
func TestStripComputedOnlyForType_TableDriven(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "computed-only fields stripped",
			in:   `{"name":"a","creationTimestamp":"2024-01-01","selfLink":"https://x/a","labelFingerprint":"abc"}`,
			want: `{"name":"a"}`,
		},
		{
			name: "optional+computed kept (network_tier)",
			in:   `{"name":"a","networkTier":"PREMIUM","creationTimestamp":"t"}`,
			want: `{"name":"a","networkTier":"PREMIUM"}`,
		},
		{
			name: "required-only kept (name)",
			in:   `{"name":"a","creationTimestamp":"t"}`,
			want: `{"name":"a"}`,
		},
		{
			name: "optional-only kept (description)",
			in:   `{"description":"d","selfLink":"x"}`,
			want: `{"description":"d"}`,
		},
		{
			name: "id stripped via universal-elide list even though schema is optional+computed",
			in:   `{"name":"a","id":"projects/x/regions/r/addresses/a"}`,
			want: `{"name":"a"}`,
		},
		{
			name: "computed-only effective_labels and terraform_labels stripped",
			in:   `{"name":"a","effectiveLabels":{"goog":"x"},"terraformLabels":{"team":"y"}}`,
			want: `{"name":"a"}`,
		},
		{
			name: "computed-only users slice stripped",
			in:   `{"name":"a","users":["projects/x/instances/i1"]}`,
			want: `{"name":"a"}`,
		},
		{
			name: "unknown-to-schema key passes through (defensive against future-schema drift)",
			in:   `{"name":"a","futureField":"value"}`,
			want: `{"name":"a","futureField":"value"}`,
		},
		{
			name: "no changes returns input unchanged",
			in:   `{"name":"a","description":"d"}`,
			want: `{"name":"a","description":"d"}`,
		},
		{
			name: "all-computed-only input collapses to empty object",
			in:   `{"creationTimestamp":"t","selfLink":"x","labelFingerprint":"f","effectiveLabels":{},"terraformLabels":{},"users":[]}`,
			want: `{}`,
		},
	}
	n := stripComputedOnlyForType("google_compute_address")
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := n(json.RawMessage(tc.in))
			require.NoError(t, err)
			assert.JSONEq(t, tc.want, string(got))
		})
	}
}

// TestStripComputedOnlyForType_NoSchema_PassesThrough pins the
// fail-open contract: an unregistered TF type returns input unchanged.
// The downstream generated.UnmarshalAttrs will already fail with "no
// registered type" on a truly-missing registration; this Normalizer
// stays out of the way rather than masking the wiring error.
func TestStripComputedOnlyForType_NoSchema_PassesThrough(t *testing.T) {
	t.Parallel()
	n := stripComputedOnlyForType("google_does_not_exist_xyz")
	in := json.RawMessage(`{"foo":"bar","creationTimestamp":"t"}`)
	out, err := n(in)
	require.NoError(t, err)
	assert.JSONEq(t, string(in), string(out))
}

// TestStripComputedOnlyForType_EmptyType pins that an empty tfType
// argument is a pass-through (defensive — no caller should be passing
// "" but a future programmer error shouldn't blow up the chain).
func TestStripComputedOnlyForType_EmptyType(t *testing.T) {
	t.Parallel()
	n := stripComputedOnlyForType("")
	in := json.RawMessage(`{"foo":"bar"}`)
	out, err := n(in)
	require.NoError(t, err)
	assert.JSONEq(t, string(in), string(out))
}

// TestStripComputedOnlyForType_EmptyPayload exercises decode-side
// pass-through: empty / null / whitespace inputs short-circuit cleanly
// without touching the schema.
func TestStripComputedOnlyForType_EmptyPayload(t *testing.T) {
	t.Parallel()
	n := stripComputedOnlyForType("google_compute_address")
	cases := []json.RawMessage{
		json.RawMessage(``),
		json.RawMessage(`null`),
		json.RawMessage(`  `),
	}
	for _, in := range cases {
		in := in
		t.Run(string(in), func(t *testing.T) {
			t.Parallel()
			out, err := n(in)
			require.NoError(t, err)
			assert.Equal(t, string(in), string(out))
		})
	}
}

// TestStripComputedOnlyForType_MalformedJSON pins the error surface:
// a non-object payload (e.g. an array or scalar at the top level)
// returns a wrapped error so the chain reports the failing helper
// instead of cascading into a confusing unmarshal error downstream.
func TestStripComputedOnlyForType_MalformedJSON(t *testing.T) {
	t.Parallel()
	n := stripComputedOnlyForType("google_compute_address")
	_, err := n(json.RawMessage(`[1,2,3]`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stripComputedOnlyForType")
}

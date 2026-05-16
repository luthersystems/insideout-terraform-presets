package imported

import (
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/policy"
)

// synthetic tfType counter — each subtest registers under a fresh name
// so the process-wide policy registry never sees a duplicate (the
// registry's unregister helper is unexported, so we rely on uniqueness
// instead).
var synthCounter atomic.Uint64

// registerSyntheticPolicy installs m under a fresh tfType name and
// returns the name. Tests address the registry through this helper so
// the production per-cloud policy files stay untouched.
func registerSyntheticPolicy(t *testing.T, m policy.Map) string {
	t.Helper()
	n := synthCounter.Add(1)
	name := fmt.Sprintf("_drift_test_synthetic_%d", n)
	policy.Register(name, m)
	return name
}

func TestCompare_NoRegisteredPolicy(t *testing.T) {
	// A tfType we never register → nil.
	got := Compare("_drift_test_never_registered", json.RawMessage(`{"a":1}`), json.RawMessage(`{"a":2}`))
	assert.Nil(t, got)
}

func TestCompare_EmptyPolicy(t *testing.T) {
	tfType := registerSyntheticPolicy(t, policy.Map{})
	got := Compare(tfType, json.RawMessage(`{"a":1}`), json.RawMessage(`{"a":2}`))
	assert.Nil(t, got)
}

func TestCompare_MalformedJSON(t *testing.T) {
	tfType := registerSyntheticPolicy(t, policy.Map{
		"versioning.enabled": {DriftSemantic: policy.DriftSemanticExact},
	})
	cases := []struct {
		name           string
		snapshot, live json.RawMessage
	}{
		{"snapshot bad", json.RawMessage(`{not json`), json.RawMessage(`{"versioning":{"enabled":true}}`)},
		{"live bad", json.RawMessage(`{"versioning":{"enabled":true}}`), json.RawMessage(`{"oops"`)},
		{"both bad", json.RawMessage(`xxx`), json.RawMessage(`yyy`)},
		{"non-object top-level", json.RawMessage(`123`), json.RawMessage(`456`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Compare(tfType, tc.snapshot, tc.live)
			assert.Nil(t, got)
		})
	}
}

func TestCompare_DriftSemanticNone_AlwaysSkipped(t *testing.T) {
	tfType := registerSyntheticPolicy(t, policy.Map{
		"name":          {DriftSemantic: policy.DriftSemanticNone},
		"uncurated_too": {}, // empty DriftSemantic — implicit DriftSemanticNone
	})
	snap := json.RawMessage(`{"name":"foo","uncurated_too":"x"}`)
	live := json.RawMessage(`{"name":"bar","uncurated_too":"y"}`)
	got := Compare(tfType, snap, live)
	assert.Nil(t, got, "DriftSemanticNone fields must never produce mismatches")
}

func TestCompare_DriftSemanticExact(t *testing.T) {
	tfType := registerSyntheticPolicy(t, policy.Map{
		"versioning.enabled": {DriftSemantic: policy.DriftSemanticExact},
		"storage_class":      {DriftSemantic: policy.DriftSemanticExact},
	})

	t.Run("identical", func(t *testing.T) {
		snap := json.RawMessage(`{"versioning":{"enabled":true},"storage_class":"STANDARD"}`)
		live := json.RawMessage(`{"versioning":{"enabled":true},"storage_class":"STANDARD"}`)
		got := Compare(tfType, snap, live)
		assert.Empty(t, got)
	})

	t.Run("nested-scalar mismatch", func(t *testing.T) {
		snap := json.RawMessage(`{"versioning":{"enabled":true},"storage_class":"STANDARD"}`)
		live := json.RawMessage(`{"versioning":{"enabled":false},"storage_class":"STANDARD"}`)
		got := Compare(tfType, snap, live)
		require.Len(t, got, 1)
		assert.Equal(t, "versioning.enabled", got[0].Field)
		assert.Equal(t, true, got[0].Snapshot)
		assert.Equal(t, false, got[0].Cloud)
	})

	t.Run("top-level-scalar mismatch", func(t *testing.T) {
		snap := json.RawMessage(`{"versioning":{"enabled":true},"storage_class":"STANDARD"}`)
		live := json.RawMessage(`{"versioning":{"enabled":true},"storage_class":"NEARLINE"}`)
		got := Compare(tfType, snap, live)
		require.Len(t, got, 1)
		assert.Equal(t, "storage_class", got[0].Field)
	})

	t.Run("both absent → no mismatch", func(t *testing.T) {
		snap := json.RawMessage(`{}`)
		live := json.RawMessage(`{}`)
		got := Compare(tfType, snap, live)
		assert.Empty(t, got)
	})

	t.Run("one side absent → mismatch", func(t *testing.T) {
		snap := json.RawMessage(`{"storage_class":"STANDARD"}`)
		live := json.RawMessage(`{}`)
		got := Compare(tfType, snap, live)
		require.Len(t, got, 1)
		assert.Equal(t, "storage_class", got[0].Field)
		assert.Equal(t, "STANDARD", got[0].Snapshot)
		assert.Nil(t, got[0].Cloud)
	})

	t.Run("singleton-list auto-unwrap on nested block", func(t *testing.T) {
		// Terraform serializes block fields as [{...}] — the resolver
		// must auto-unwrap one-element lists so "versioning.enabled"
		// keeps resolving against the on-disk shape.
		snap := json.RawMessage(`{"versioning":[{"enabled":true}]}`)
		live := json.RawMessage(`{"versioning":[{"enabled":false}]}`)
		got := Compare(tfType, snap, live)
		require.Len(t, got, 1)
		assert.Equal(t, "versioning.enabled", got[0].Field)
	})
}

func TestCompare_DriftSemanticWholeList(t *testing.T) {
	tfType := registerSyntheticPolicy(t, policy.Map{
		"lifecycle_rule": {DriftSemantic: policy.DriftSemanticWholeList},
	})

	rule := func(age int, cls string) string {
		return fmt.Sprintf(`{"action":{"type":"SetStorageClass","storage_class":%q},"condition":{"age":%d}}`, cls, age)
	}

	t.Run("matching list → no mismatch", func(t *testing.T) {
		body := fmt.Sprintf(`{"lifecycle_rule":[%s,%s]}`, rule(30, "NEARLINE"), rule(90, "COLDLINE"))
		got := Compare(tfType, json.RawMessage(body), json.RawMessage(body))
		assert.Empty(t, got)
	})

	t.Run("differing list → mismatch", func(t *testing.T) {
		snap := fmt.Sprintf(`{"lifecycle_rule":[%s]}`, rule(30, "NEARLINE"))
		live := fmt.Sprintf(`{"lifecycle_rule":[%s]}`, rule(60, "NEARLINE"))
		got := Compare(tfType, json.RawMessage(snap), json.RawMessage(live))
		require.Len(t, got, 1)
		assert.Equal(t, "lifecycle_rule", got[0].Field)
		// Snapshot/Cloud must be lists, not raw objects.
		assert.IsType(t, []any{}, got[0].Snapshot)
		assert.IsType(t, []any{}, got[0].Cloud)
	})

	t.Run("order-sensitive", func(t *testing.T) {
		snap := fmt.Sprintf(`{"lifecycle_rule":[%s,%s]}`, rule(30, "NEARLINE"), rule(90, "COLDLINE"))
		live := fmt.Sprintf(`{"lifecycle_rule":[%s,%s]}`, rule(90, "COLDLINE"), rule(30, "NEARLINE"))
		got := Compare(tfType, json.RawMessage(snap), json.RawMessage(live))
		require.Len(t, got, 1, "WholeList must be order-sensitive")
	})

	t.Run("empty-list both sides → no mismatch", func(t *testing.T) {
		body := `{"lifecycle_rule":[]}`
		got := Compare(tfType, json.RawMessage(body), json.RawMessage(body))
		assert.Empty(t, got)
	})

	t.Run("absent both sides → no mismatch", func(t *testing.T) {
		got := Compare(tfType, json.RawMessage(`{}`), json.RawMessage(`{}`))
		assert.Empty(t, got)
	})
}

func TestCompare_DriftSemanticLabelFilter(t *testing.T) {
	// Default-prefixes policy: when LabelDriftIgnorePrefixes is left
	// unset, the comparator falls back to {"goog-", "goog_"} for
	// back-compat with policies authored before the per-policy knob.
	tfType := registerSyntheticPolicy(t, policy.Map{
		"labels": {DriftSemantic: policy.DriftSemanticLabelFilter},
	})

	t.Run("goog-* keys filtered, user keys match → no mismatch", func(t *testing.T) {
		snap := json.RawMessage(`{"labels":{"env":"prod","team":"infra"}}`)
		live := json.RawMessage(`{"labels":{"env":"prod","team":"infra","goog-managed-by":"composer","goog_terraform_provisioned":"true"}}`)
		got := Compare(tfType, snap, live)
		assert.Empty(t, got, "goog-/goog_ prefixed keys must be filtered before compare")
	})

	t.Run("user-key drift emits one per-key mismatch, goog-* noise filtered", func(t *testing.T) {
		snap := json.RawMessage(`{"labels":{"env":"prod","goog-managed-by":"x"}}`)
		live := json.RawMessage(`{"labels":{"env":"staging","goog-managed-by":"y"}}`)
		got := Compare(tfType, snap, live)
		require.Len(t, got, 1, "expected one per-key mismatch on labels.env")
		assert.Equal(t, "labels.env", got[0].Field)
		assert.Equal(t, "prod", got[0].Snapshot)
		assert.Equal(t, "staging", got[0].Cloud)
	})

	t.Run("missing-on-cloud emits per-key mismatch with empty Cloud", func(t *testing.T) {
		snap := json.RawMessage(`{"labels":{"env":"prod","team":"infra"}}`)
		live := json.RawMessage(`{"labels":{"env":"prod"}}`)
		got := Compare(tfType, snap, live)
		require.Len(t, got, 1)
		assert.Equal(t, "labels.team", got[0].Field)
		assert.Equal(t, "infra", got[0].Snapshot)
		assert.Equal(t, "", got[0].Cloud)
	})

	t.Run("missing-on-snapshot emits per-key mismatch with empty Snapshot", func(t *testing.T) {
		snap := json.RawMessage(`{"labels":{"env":"prod"}}`)
		live := json.RawMessage(`{"labels":{"env":"prod","new":"v"}}`)
		got := Compare(tfType, snap, live)
		require.Len(t, got, 1)
		assert.Equal(t, "labels.new", got[0].Field)
		assert.Equal(t, "", got[0].Snapshot)
		assert.Equal(t, "v", got[0].Cloud)
	})

	t.Run("multiple user-key drifts emit sorted per-key mismatches", func(t *testing.T) {
		snap := json.RawMessage(`{"labels":{"env":"prod","team":"infra","goog-x":"1"}}`)
		live := json.RawMessage(`{"labels":{"env":"staging","team":"platform","goog-x":"2"}}`)
		got := Compare(tfType, snap, live)
		require.Len(t, got, 2)
		assert.Equal(t, "labels.env", got[0].Field)
		assert.Equal(t, "labels.team", got[1].Field)
	})

	t.Run("absent both sides → no mismatch", func(t *testing.T) {
		got := Compare(tfType, json.RawMessage(`{}`), json.RawMessage(`{}`))
		assert.Empty(t, got)
	})

	t.Run("only goog-* keys → empty after filter → no mismatch", func(t *testing.T) {
		snap := json.RawMessage(`{"labels":{"goog-a":"1"}}`)
		live := json.RawMessage(`{"labels":{"goog_b":"2"}}`)
		got := Compare(tfType, snap, live)
		assert.Empty(t, got)
	})
}

func TestCompare_DriftSemanticLabelFilter_PerPolicyPrefixes(t *testing.T) {
	// Per-policy ignore prefixes: extends the default goog- set with
	// reliable's "insideout-import" provenance prefix so a re-emission
	// that bumps the import-session label doesn't surface as drift.
	tfType := registerSyntheticPolicy(t, policy.Map{
		"labels": {
			DriftSemantic:            policy.DriftSemanticLabelFilter,
			LabelDriftIgnorePrefixes: []string{"goog-", "goog_", "insideout-import"},
		},
	})

	t.Run("insideout-import* prefix filtered alongside goog-*", func(t *testing.T) {
		snap := json.RawMessage(`{"labels":{
			"env": "prod",
			"goog-managed-by": "composer-A",
			"insideout-imported": "2026-01-01",
			"insideout-import-session": "sess_v2_aaa"
		}}`)
		live := json.RawMessage(`{"labels":{
			"env": "prod",
			"goog-managed-by": "composer-B",
			"insideout-imported": "2026-05-15",
			"insideout-import-session": "sess_v2_bbb"
		}}`)
		got := Compare(tfType, snap, live)
		assert.Empty(t, got, "all diffs are on filtered prefixes")
	})

	t.Run("user-key diff still emitted when only the prefixes match noise", func(t *testing.T) {
		snap := json.RawMessage(`{"labels":{"env":"prod","insideout-imported":"x"}}`)
		live := json.RawMessage(`{"labels":{"env":"staging","insideout-imported":"y"}}`)
		got := Compare(tfType, snap, live)
		require.Len(t, got, 1)
		assert.Equal(t, "labels.env", got[0].Field)
	})
}

func TestCompare_DriftSemanticLabelFilter_NestedPath(t *testing.T) {
	// A label-shaped attribute nested inside a singleton block — the
	// per-key Field must use the policy path as its parent so the
	// resulting field name reads as `metadata.labels.env`.
	tfType := registerSyntheticPolicy(t, policy.Map{
		"metadata.labels": {DriftSemantic: policy.DriftSemanticLabelFilter},
	})
	snap := json.RawMessage(`{"metadata":[{"labels":{"env":"prod"}}]}`)
	live := json.RawMessage(`{"metadata":[{"labels":{"env":"staging"}}]}`)
	got := Compare(tfType, snap, live)
	require.Len(t, got, 1)
	assert.Equal(t, "metadata.labels.env", got[0].Field)
}

func TestCompare_SortedOutput(t *testing.T) {
	// Multiple fields with mismatches must come back sorted by Field
	// — the underlying map iteration is non-deterministic; the
	// sort.Slice in Compare is what holds the golden contract.
	tfType := registerSyntheticPolicy(t, policy.Map{
		"zeta":  {DriftSemantic: policy.DriftSemanticExact},
		"alpha": {DriftSemantic: policy.DriftSemanticExact},
		"mike":  {DriftSemantic: policy.DriftSemanticExact},
	})
	snap := json.RawMessage(`{"alpha":1,"mike":2,"zeta":3}`)
	live := json.RawMessage(`{"alpha":10,"mike":20,"zeta":30}`)
	got := Compare(tfType, snap, live)
	require.Len(t, got, 3)
	assert.Equal(t, "alpha", got[0].Field)
	assert.Equal(t, "mike", got[1].Field)
	assert.Equal(t, "zeta", got[2].Field)
}

func TestCompare_MixedSemantics(t *testing.T) {
	// One policy with several axes at once — exercises the dispatch
	// switch end-to-end and confirms None entries never leak into
	// output even when their value differs. LabelFilter now emits one
	// per-key entry, so the labels.env diff appears as `labels.env`.
	tfType := registerSyntheticPolicy(t, policy.Map{
		"name":               {}, // None
		"versioning.enabled": {DriftSemantic: policy.DriftSemanticExact},
		"lifecycle_rule":     {DriftSemantic: policy.DriftSemanticWholeList},
		"labels":             {DriftSemantic: policy.DriftSemanticLabelFilter},
	})
	snap := json.RawMessage(`{
		"name": "bucket-a",
		"versioning": {"enabled": true},
		"lifecycle_rule": [{"action":{"type":"Delete"},"condition":{"age":7}}],
		"labels": {"env":"prod","goog-managed-by":"x"}
	}`)
	live := json.RawMessage(`{
		"name": "bucket-b",
		"versioning": {"enabled": false},
		"lifecycle_rule": [{"action":{"type":"Delete"},"condition":{"age":30}}],
		"labels": {"env":"staging","goog_managed_by":"y"}
	}`)
	got := Compare(tfType, snap, live)
	require.Len(t, got, 3)
	// Sorted: labels.env, lifecycle_rule, versioning.enabled.
	assert.Equal(t, "labels.env", got[0].Field)
	assert.Equal(t, "lifecycle_rule", got[1].Field)
	assert.Equal(t, "versioning.enabled", got[2].Field)
}

func TestResolvePath(t *testing.T) {
	// Direct exercise of the path resolver — covers shapes that
	// Compare exercises indirectly via the Exact path.
	cases := []struct {
		name    string
		path    string
		in      string
		wantVal any
		wantOK  bool
	}{
		{"flat scalar", "a", `{"a":"x"}`, "x", true},
		{"nested map", "a.b", `{"a":{"b":"x"}}`, "x", true},
		{"singleton-list unwrap", "a.b", `{"a":[{"b":"x"}]}`, "x", true},
		{"multi-element list stops", "a.b", `{"a":[{"b":"x"},{"b":"y"}]}`, nil, false},
		{"missing leaf", "a.b", `{"a":{}}`, nil, false},
		{"missing root", "a.b", `{}`, nil, false},
		{"empty path", "", `{"a":"x"}`, nil, false},
		{"into scalar", "a.b", `{"a":"scalar"}`, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var m map[string]any
			require.NoError(t, json.Unmarshal([]byte(tc.in), &m))
			got, ok := resolvePath(tc.path, m)
			assert.Equal(t, tc.wantOK, ok)
			if tc.wantOK {
				assert.Equal(t, tc.wantVal, got)
			}
		})
	}
}

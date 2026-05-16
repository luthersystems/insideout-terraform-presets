package awsdiscover

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// asMap parses a JSON-encoded object payload for assertion comparison.
// Keeps test bodies focused on the post-normalize shape rather than
// on Unmarshal boilerplate.
func asMap(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	if len(raw) == 0 {
		return nil
	}
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	return m
}

// TestChain pins the composition contract: nil-friendly construction,
// in-order application, and error short-circuit. The empty / all-nil
// chain is the no-op fallback that registration sites lean on when
// building a chain conditionally.
func TestChain(t *testing.T) {
	t.Parallel()

	t.Run("empty chain is identity", func(t *testing.T) {
		t.Parallel()
		n := chain()
		in := json.RawMessage(`{"X":1}`)
		out, err := n(in)
		require.NoError(t, err)
		assert.JSONEq(t, `{"X":1}`, string(out))
	})

	t.Run("all-nil chain is identity", func(t *testing.T) {
		t.Parallel()
		n := chain(nil, nil)
		in := json.RawMessage(`{"X":1}`)
		out, err := n(in)
		require.NoError(t, err)
		assert.JSONEq(t, `{"X":1}`, string(out))
	})

	t.Run("single non-nil collapses to itself", func(t *testing.T) {
		t.Parallel()
		n := chain(nil, renameField("X", "Y"), nil)
		out, err := n(json.RawMessage(`{"X":1}`))
		require.NoError(t, err)
		assert.JSONEq(t, `{"Y":1}`, string(out))
	})

	t.Run("multi-step ordered application", func(t *testing.T) {
		t.Parallel()
		n := chain(
			renameField("A", "B"),
			renameField("B", "C"),
		)
		out, err := n(json.RawMessage(`{"A":42}`))
		require.NoError(t, err)
		assert.JSONEq(t, `{"C":42}`, string(out))
	})

	t.Run("error short-circuits with step index", func(t *testing.T) {
		t.Parallel()
		boom := errors.New("boom")
		failing := func(_ json.RawMessage) (json.RawMessage, error) { return nil, boom }
		n := chain(renameField("A", "B"), failing, renameField("B", "C"))
		_, err := n(json.RawMessage(`{"A":1}`))
		require.Error(t, err)
		assert.ErrorIs(t, err, boom)
		assert.Contains(t, err.Error(), "normalizer step 1")
	})
}

// TestRenameField pins the rename contract: absent source is a no-op,
// already-present target is a no-op (no clobber), identity rename is
// a no-op, and a malformed payload surfaces as an error.
func TestRenameField(t *testing.T) {
	t.Parallel()

	t.Run("renames present field", func(t *testing.T) {
		t.Parallel()
		out, err := renameField("LogGroupName", "Name")(json.RawMessage(`{"LogGroupName":"/aws/x","Arn":"a"}`))
		require.NoError(t, err)
		m := asMap(t, out)
		assert.Equal(t, "/aws/x", m["Name"])
		assert.NotContains(t, m, "LogGroupName")
		assert.Equal(t, "a", m["Arn"])
	})

	t.Run("absent source is no-op", func(t *testing.T) {
		t.Parallel()
		in := json.RawMessage(`{"Arn":"a"}`)
		out, err := renameField("LogGroupName", "Name")(in)
		require.NoError(t, err)
		assert.JSONEq(t, `{"Arn":"a"}`, string(out))
	})

	t.Run("preserves target when both present", func(t *testing.T) {
		t.Parallel()
		// Target wins — the rename leaves both intact so a hand-shaped
		// payload upstream is never silently clobbered.
		out, err := renameField("LogGroupName", "Name")(json.RawMessage(`{"LogGroupName":"a","Name":"b"}`))
		require.NoError(t, err)
		m := asMap(t, out)
		assert.Equal(t, "a", m["LogGroupName"])
		assert.Equal(t, "b", m["Name"])
	})

	t.Run("identity rename is no-op", func(t *testing.T) {
		t.Parallel()
		out, err := renameField("Name", "Name")(json.RawMessage(`{"Name":"x"}`))
		require.NoError(t, err)
		assert.JSONEq(t, `{"Name":"x"}`, string(out))
	})

	t.Run("empty args are no-op", func(t *testing.T) {
		t.Parallel()
		in := json.RawMessage(`{"X":1}`)
		out, err := renameField("", "Y")(in)
		require.NoError(t, err)
		assert.JSONEq(t, `{"X":1}`, string(out))
		out, err = renameField("X", "")(in)
		require.NoError(t, err)
		assert.JSONEq(t, `{"X":1}`, string(out))
	})

	t.Run("malformed JSON returns error", func(t *testing.T) {
		t.Parallel()
		_, err := renameField("A", "B")(json.RawMessage(`{not json`))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "renameField")
	})

	t.Run("null payload is no-op", func(t *testing.T) {
		t.Parallel()
		out, err := renameField("A", "B")(json.RawMessage(`null`))
		require.NoError(t, err)
		assert.Equal(t, "null", string(out))
	})

	t.Run("empty payload is no-op", func(t *testing.T) {
		t.Parallel()
		out, err := renameField("A", "B")(json.RawMessage(``))
		require.NoError(t, err)
		assert.Equal(t, 0, len(out))
	})
}

// TestFlattenTagList pins the tag-list flatten contract: list of
// {Key,Value} collapses to a flat map, absent or already-flat input
// is a no-op, and a malformed shape surfaces as an error.
func TestFlattenTagList(t *testing.T) {
	t.Parallel()

	t.Run("flattens list of Key/Value pairs", func(t *testing.T) {
		t.Parallel()
		in := json.RawMessage(`{"Tags":[{"Key":"Project","Value":"io-abc"},{"Key":"Env","Value":"prod"}]}`)
		out, err := flattenTagList("Tags")(in)
		require.NoError(t, err)
		m := asMap(t, out)
		// flattenTagList wraps the flat map under the verbatim
		// marker (see #501 — tag keys are user data and must not be
		// CamelCase-rewritten by the downstream shape transform).
		wrapper, ok := m["Tags"].(map[string]any)
		require.True(t, ok, "Tags should be a map, got %T", m["Tags"])
		tagsAny, ok := wrapper["__verbatim__"]
		require.True(t, ok, "Tags should be under __verbatim__ wrapper")
		tags, ok := tagsAny.(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "io-abc", tags["Project"])
		assert.Equal(t, "prod", tags["Env"])
	})

	t.Run("empty list flattens to empty map under verbatim wrapper", func(t *testing.T) {
		t.Parallel()
		out, err := flattenTagList("Tags")(json.RawMessage(`{"Tags":[]}`))
		require.NoError(t, err)
		m := asMap(t, out)
		wrapper, ok := m["Tags"].(map[string]any)
		require.True(t, ok)
		tagsAny, ok := wrapper["__verbatim__"]
		require.True(t, ok)
		tags, ok := tagsAny.(map[string]any)
		require.True(t, ok)
		assert.Len(t, tags, 0)
	})

	t.Run("absent key is no-op", func(t *testing.T) {
		t.Parallel()
		in := json.RawMessage(`{"Arn":"a"}`)
		out, err := flattenTagList("Tags")(in)
		require.NoError(t, err)
		assert.JSONEq(t, `{"Arn":"a"}`, string(out))
	})

	t.Run("bare-flat shape gets verbatim wrapper", func(t *testing.T) {
		t.Parallel()
		// A bare flat tag map (no wrapper) is wrapped so the
		// downstream shape transform doesn't corrupt the user-data
		// keys. Catches the case where a hand-built payload or a
		// prior pass produced a map without the verbatim marker.
		in := json.RawMessage(`{"Tags":{"Project":"io-abc"}}`)
		out, err := flattenTagList("Tags")(in)
		require.NoError(t, err)
		m := asMap(t, out)
		wrapper, ok := m["Tags"].(map[string]any)
		require.True(t, ok)
		tags, ok := wrapper["__verbatim__"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "io-abc", tags["Project"])
	})

	t.Run("already-wrapped is no-op (idempotent)", func(t *testing.T) {
		t.Parallel()
		// Already wrapped under the verbatim marker — passes through
		// unchanged so chain composition is safe to re-apply.
		in := json.RawMessage(`{"Tags":{"__verbatim__":{"Project":"io-abc"}}}`)
		out, err := flattenTagList("Tags")(in)
		require.NoError(t, err)
		assert.JSONEq(t, `{"Tags":{"__verbatim__":{"Project":"io-abc"}}}`, string(out))
	})

	t.Run("skips malformed entries", func(t *testing.T) {
		t.Parallel()
		// Missing Key, non-string Key, and a non-object entry are
		// silently dropped — defensive against partial payloads.
		in := json.RawMessage(`{"Tags":[
			{"Key":"OK","Value":"yes"},
			{"Value":"no-key"},
			{"Key":"","Value":"empty-key"},
			"bare-string",
			{"Key":"AlsoOK","Value":""}
		]}`)
		out, err := flattenTagList("Tags")(in)
		require.NoError(t, err)
		m := asMap(t, out)
		wrapper, ok := m["Tags"].(map[string]any)
		require.True(t, ok)
		tags, ok := wrapper["__verbatim__"].(map[string]any)
		require.True(t, ok)
		assert.Len(t, tags, 2)
		assert.Equal(t, "yes", tags["OK"])
		// Empty Value preserved.
		assert.Equal(t, "", tags["AlsoOK"])
	})

	t.Run("unexpected shape errors", func(t *testing.T) {
		t.Parallel()
		in := json.RawMessage(`{"Tags":"oops"}`)
		_, err := flattenTagList("Tags")(in)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unexpected shape")
	})

	t.Run("malformed JSON returns error", func(t *testing.T) {
		t.Parallel()
		_, err := flattenTagList("Tags")(json.RawMessage(`{not json`))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "flattenTagList")
	})

	t.Run("empty key is no-op", func(t *testing.T) {
		t.Parallel()
		in := json.RawMessage(`{"X":1}`)
		out, err := flattenTagList("")(in)
		require.NoError(t, err)
		assert.JSONEq(t, `{"X":1}`, string(out))
	})

	t.Run("null payload is no-op", func(t *testing.T) {
		t.Parallel()
		out, err := flattenTagList("Tags")(json.RawMessage(`null`))
		require.NoError(t, err)
		assert.Equal(t, "null", string(out))
	})
}

// TestTrimARNStar pins the trim contract: trailing ":*" stripped,
// absent or non-string field is a no-op, no-suffix string is a no-op,
// and a malformed payload errors.
func TestTrimARNStar(t *testing.T) {
	t.Parallel()

	t.Run("strips trailing colon-star", func(t *testing.T) {
		t.Parallel()
		in := json.RawMessage(`{"Arn":"arn:aws:logs:us-east-1:123:log-group:/aws/x:*"}`)
		out, err := trimARNStar("Arn")(in)
		require.NoError(t, err)
		m := asMap(t, out)
		assert.Equal(t, "arn:aws:logs:us-east-1:123:log-group:/aws/x", m["Arn"])
	})

	t.Run("no suffix is no-op", func(t *testing.T) {
		t.Parallel()
		in := json.RawMessage(`{"Arn":"arn:aws:logs:us-east-1:123:log-group:/aws/x"}`)
		out, err := trimARNStar("Arn")(in)
		require.NoError(t, err)
		assert.JSONEq(t, `{"Arn":"arn:aws:logs:us-east-1:123:log-group:/aws/x"}`, string(out))
	})

	t.Run("absent key is no-op", func(t *testing.T) {
		t.Parallel()
		in := json.RawMessage(`{"X":1}`)
		out, err := trimARNStar("Arn")(in)
		require.NoError(t, err)
		assert.JSONEq(t, `{"X":1}`, string(out))
	})

	t.Run("non-string value is no-op", func(t *testing.T) {
		t.Parallel()
		in := json.RawMessage(`{"Arn":123}`)
		out, err := trimARNStar("Arn")(in)
		require.NoError(t, err)
		assert.JSONEq(t, `{"Arn":123}`, string(out))
	})

	t.Run("null value is no-op", func(t *testing.T) {
		t.Parallel()
		in := json.RawMessage(`{"Arn":null}`)
		out, err := trimARNStar("Arn")(in)
		require.NoError(t, err)
		assert.JSONEq(t, `{"Arn":null}`, string(out))
	})

	t.Run("malformed JSON returns error", func(t *testing.T) {
		t.Parallel()
		_, err := trimARNStar("Arn")(json.RawMessage(`{not json`))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "trimARNStar")
	})

	t.Run("empty key is no-op", func(t *testing.T) {
		t.Parallel()
		in := json.RawMessage(`{"X":1}`)
		out, err := trimARNStar("")(in)
		require.NoError(t, err)
		assert.JSONEq(t, `{"X":1}`, string(out))
	})
}

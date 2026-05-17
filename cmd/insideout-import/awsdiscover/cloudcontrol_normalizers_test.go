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

	t.Run("missing Value drops entry", func(t *testing.T) {
		t.Parallel()
		// A CFN tag entry like {"Key":"OnlyKey"} (no Value) would
		// otherwise produce flat["OnlyKey"] = nil, which json-
		// marshals to a bare `null` and fails downstream
		// Value[string].UnmarshalJSON (issue #575). The entry is
		// dropped, matching the empty-Key / non-object skip paths.
		in := json.RawMessage(`{"Tags":[
			{"Key":"OnlyKey"},
			{"Key":"K","Value":"V"}
		]}`)
		out, err := flattenTagList("Tags")(in)
		require.NoError(t, err)
		m := asMap(t, out)
		wrapper, ok := m["Tags"].(map[string]any)
		require.True(t, ok)
		tags, ok := wrapper["__verbatim__"].(map[string]any)
		require.True(t, ok)
		assert.Len(t, tags, 1)
		assert.Equal(t, "V", tags["K"])
		_, hasOnlyKey := tags["OnlyKey"]
		assert.False(t, hasOnlyKey, "OnlyKey must be dropped to avoid bare-null Tags[k]")
	})

	t.Run("Value is null drops entry", func(t *testing.T) {
		t.Parallel()
		// Explicit-null Value is treated the same as absent Value
		// (issue #575): drop the entry instead of producing a
		// bare-null Tags[k].
		in := json.RawMessage(`{"Tags":[
			{"Key":"NullVal","Value":null},
			{"Key":"K","Value":"V"}
		]}`)
		out, err := flattenTagList("Tags")(in)
		require.NoError(t, err)
		m := asMap(t, out)
		wrapper, ok := m["Tags"].(map[string]any)
		require.True(t, ok)
		tags, ok := wrapper["__verbatim__"].(map[string]any)
		require.True(t, ok)
		assert.Len(t, tags, 1)
		assert.Equal(t, "V", tags["K"])
		_, hasNullVal := tags["NullVal"]
		assert.False(t, hasNullVal, "NullVal must be dropped to avoid bare-null Tags[k]")
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

// TestSynthIDFromField pins the id-synthesis contract used by #502 to
// land the hand-rolled `id == name` invariant on the generic Cloud
// Control path. Covers: present source copies into `Id`, absent source
// is a no-op, already-present `Id` is preserved (no clobber),
// non-string source is a no-op (the helper expects a string scalar),
// and malformed payloads error cleanly.
func TestSynthIDFromField(t *testing.T) {
	t.Parallel()

	t.Run("copies post-rename Name into Id", func(t *testing.T) {
		t.Parallel()
		// Typical chain shape: renameField has already landed `Name`
		// from `LogGroupName`; synthIDFromField copies it to `Id` so
		// the downstream camelToSnake projection produces `id` on the
		// shaped payload.
		in := json.RawMessage(`{"Name":"/aws/lambda/demo","Arn":"a"}`)
		out, err := synthIDFromField("Name")(in)
		require.NoError(t, err)
		m := asMap(t, out)
		assert.Equal(t, "/aws/lambda/demo", m["Name"])
		assert.Equal(t, "/aws/lambda/demo", m["Id"], "Id should mirror Name")
		assert.Equal(t, "a", m["Arn"])
	})

	t.Run("absent source is no-op", func(t *testing.T) {
		t.Parallel()
		in := json.RawMessage(`{"Arn":"a"}`)
		out, err := synthIDFromField("Name")(in)
		require.NoError(t, err)
		assert.JSONEq(t, `{"Arn":"a"}`, string(out))
	})

	t.Run("preserves existing Id (no clobber)", func(t *testing.T) {
		t.Parallel()
		// If `Id` is already on the payload (e.g. a hand-shaped CFN
		// response or a prior synth pass), leave it intact — matches
		// renameField's no-clobber convention.
		in := json.RawMessage(`{"Name":"/aws/x","Id":"keep-me"}`)
		out, err := synthIDFromField("Name")(in)
		require.NoError(t, err)
		m := asMap(t, out)
		assert.Equal(t, "keep-me", m["Id"])
		assert.Equal(t, "/aws/x", m["Name"])
	})

	t.Run("non-string source is no-op", func(t *testing.T) {
		t.Parallel()
		in := json.RawMessage(`{"Name":123}`)
		out, err := synthIDFromField("Name")(in)
		require.NoError(t, err)
		m := asMap(t, out)
		assert.NotContains(t, m, "Id")
	})

	t.Run("empty string source is no-op", func(t *testing.T) {
		t.Parallel()
		in := json.RawMessage(`{"Name":""}`)
		out, err := synthIDFromField("Name")(in)
		require.NoError(t, err)
		m := asMap(t, out)
		assert.NotContains(t, m, "Id")
	})

	t.Run("null source is no-op", func(t *testing.T) {
		t.Parallel()
		in := json.RawMessage(`{"Name":null}`)
		out, err := synthIDFromField("Name")(in)
		require.NoError(t, err)
		m := asMap(t, out)
		assert.NotContains(t, m, "Id")
	})

	t.Run("empty src is no-op", func(t *testing.T) {
		t.Parallel()
		in := json.RawMessage(`{"Name":"x"}`)
		out, err := synthIDFromField("")(in)
		require.NoError(t, err)
		assert.JSONEq(t, `{"Name":"x"}`, string(out))
	})

	t.Run("null payload is no-op", func(t *testing.T) {
		t.Parallel()
		out, err := synthIDFromField("Name")(json.RawMessage(`null`))
		require.NoError(t, err)
		assert.Equal(t, "null", string(out))
	})

	t.Run("malformed JSON returns error", func(t *testing.T) {
		t.Parallel()
		_, err := synthIDFromField("Name")(json.RawMessage(`{not json`))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "synthIDFromField")
	})

	t.Run("composes after renameField in chain", func(t *testing.T) {
		t.Parallel()
		// End-to-end production-shape: a CFN payload with `LogGroupName`
		// flows through renameField → synthIDFromField and lands both
		// `Name` and `Id` for the downstream Layer-1 unmarshal.
		n := chain(
			renameField("LogGroupName", "Name"),
			synthIDFromField("Name"),
		)
		out, err := n(json.RawMessage(`{"LogGroupName":"/aws/x"}`))
		require.NoError(t, err)
		m := asMap(t, out)
		assert.Equal(t, "/aws/x", m["Name"])
		assert.Equal(t, "/aws/x", m["Id"])
		assert.NotContains(t, m, "LogGroupName")
	})
}

// TestStripComputedOnlyForType_TableDriven exercises the #582 generic
// computed-only filter against the real generated.AWSS3Bucket schema.
// s3_bucket is chosen as the representative type because its schema
// covers all four flag combinations the helper must distinguish:
// required-only (none; AWS s3 has no Required-only fields, so we use a
// synthetic example with the SQS schema below to cover that), optional-
// only (force_destroy), optional+computed (bucket, name, acl, policy,
// region — but region is actually Computed-only here, see below),
// computed-only (arn, bucket_domain_name, bucket_regional_domain_name,
// hosted_zone_id, website_domain, website_endpoint, region — these are
// all server-set on read). Combined with `id` (Optional+Computed but on
// the universal-elide allowlist), this single type tables out every
// branch in stripComputedOnlyForType.
//
// Tests against the LIVE schema registration (not a fixture) so a
// future regeneration of aws_s3_bucket.gen.go that flips a flag from
// Computed-only to Optional+Computed fails this test immediately —
// guarding against silent parity drift.
//
// Inputs use PascalCase keys (the CFN wire shape the Normalizer
// receives BEFORE shapeCFNForLayer1 runs); the helper internally
// camelToSnake-renames each key for the schema lookup.
func TestStripComputedOnlyForType_TableDriven(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "computed-only fields stripped (s3_bucket arn, bucket_domain_name, hosted_zone_id)",
			in:   `{"Bucket":"b","Arn":"arn:aws:s3:::b","BucketDomainName":"b.s3.amazonaws.com","HostedZoneId":"Z3","Region":"us-east-1"}`,
			want: `{"Bucket":"b"}`,
		},
		{
			name: "optional+computed kept (bucket)",
			in:   `{"Bucket":"b","Arn":"arn:aws:s3:::b"}`,
			want: `{"Bucket":"b"}`,
		},
		{
			name: "optional-only kept (force_destroy)",
			in:   `{"ForceDestroy":false,"Arn":"x"}`,
			want: `{"ForceDestroy":false}`,
		},
		{
			// AWS-side diverges from GCP: `id` is NOT in
			// universallyElidedTFFields here (see that variable's godoc
			// — AWS hand-rolled enrichers synthesize ID via
			// synthIDFromField, so universal elide would break parity).
			// `id` is Optional+Computed → Configurable → kept.
			name: "id kept (Optional+Computed, not on AWS universal-elide list)",
			in:   `{"Bucket":"b","Id":"b"}`,
			want: `{"Bucket":"b","Id":"b"}`,
		},
		{
			name: "computed-only website_domain and website_endpoint stripped",
			in:   `{"Bucket":"b","WebsiteDomain":"b.s3-website-us-east-1.amazonaws.com","WebsiteEndpoint":"http://b/"}`,
			want: `{"Bucket":"b"}`,
		},
		{
			name: "computed-only bucket_regional_domain_name stripped",
			in:   `{"Bucket":"b","BucketRegionalDomainName":"b.s3.us-east-1.amazonaws.com"}`,
			want: `{"Bucket":"b"}`,
		},
		{
			name: "unknown-to-schema key passes through (defensive against future-schema drift)",
			in:   `{"Bucket":"b","FutureField":"value"}`,
			want: `{"Bucket":"b","FutureField":"value"}`,
		},
		{
			name: "no changes returns input unchanged",
			in:   `{"Bucket":"b","ForceDestroy":false}`,
			want: `{"Bucket":"b","ForceDestroy":false}`,
		},
		{
			name: "all-computed-only input collapses to empty object",
			in:   `{"Arn":"x","BucketDomainName":"d","HostedZoneId":"z","Region":"r","WebsiteDomain":"w","WebsiteEndpoint":"e"}`,
			want: `{}`,
		},
	}
	n := stripComputedOnlyForType("aws_s3_bucket")
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

// TestStripComputedOnlyForType_SQSQueue_URLArn pins parity on the
// canonical SQS computed-only fields (`Arn`, `Url`) — distinct from
// the s3_bucket table because SQS routes `URL` through the unusual
// json-tag `url` (lowercase) and the schema lookup must still match
// via the camelToSnake bridge. Also exercises the post-rename shape
// the production chain produces: a CFN payload that's been
// renameField'd from `QueueName`/`MessageRetentionPeriod`/
// `VisibilityTimeout` to the TF Layer-1 PascalCase aliases (`Name`,
// `MessageRetentionSeconds`, `VisibilityTimeoutSeconds`) which then
// snake_case to fields the schema knows about.
func TestStripComputedOnlyForType_SQSQueue_URLArn(t *testing.T) {
	t.Parallel()
	n := stripComputedOnlyForType("aws_sqs_queue")

	t.Run("Arn and Url stripped; user-set fields kept", func(t *testing.T) {
		t.Parallel()
		// Post-rename shape: QueueName → Name,
		// MessageRetentionPeriod → MessageRetentionSeconds.
		in := json.RawMessage(`{"Name":"q","Arn":"arn:aws:sqs:us-east-1:1:q","Url":"https://sqs.us-east-1.amazonaws.com/1/q","MessageRetentionSeconds":345600}`)
		out, err := n(in)
		require.NoError(t, err)
		assert.JSONEq(t, `{"Name":"q","MessageRetentionSeconds":345600}`, string(out))
	})

	t.Run("optional+computed kms_master_key_id and tags_all flags handled correctly", func(t *testing.T) {
		t.Parallel()
		// kms_master_key_id is Optional (no Computed) → kept.
		// tags_all is Optional+Computed → kept (Configurable()).
		// arn (Computed-only) → stripped.
		in := json.RawMessage(`{"KmsMasterKeyId":"alias/x","TagsAll":{"k":"v"},"Arn":"x"}`)
		out, err := n(in)
		require.NoError(t, err)
		assert.JSONEq(t, `{"KmsMasterKeyId":"alias/x","TagsAll":{"k":"v"}}`, string(out))
	})
}

// TestStripComputedOnlyForType_NoSchema_PassesThrough pins the
// fail-open contract: an unregistered TF type returns input unchanged.
// The downstream generated.UnmarshalAttrs will already fail with "no
// registered type" on a truly-missing registration; this Normalizer
// stays out of the way rather than masking the wiring error.
func TestStripComputedOnlyForType_NoSchema_PassesThrough(t *testing.T) {
	t.Parallel()
	n := stripComputedOnlyForType("aws_does_not_exist_xyz")
	in := json.RawMessage(`{"Foo":"bar","Arn":"x"}`)
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
	in := json.RawMessage(`{"Foo":"bar"}`)
	out, err := n(in)
	require.NoError(t, err)
	assert.JSONEq(t, string(in), string(out))
}

// TestStripComputedOnlyForType_EmptyPayload exercises decode-side
// pass-through: empty / null / whitespace inputs short-circuit cleanly
// without touching the schema.
func TestStripComputedOnlyForType_EmptyPayload(t *testing.T) {
	t.Parallel()
	n := stripComputedOnlyForType("aws_s3_bucket")
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
	n := stripComputedOnlyForType("aws_s3_bucket")
	_, err := n(json.RawMessage(`[1,2,3]`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stripComputedOnlyForType")
}

// TestStripComputedOnlyForType_ComposesAfterRenameAndSynth exercises
// the production chain shape (#502 / #582): renameField →
// synthIDFromField → trimARNStar → stripComputedOnlyForType. Pins
// that placing the strip LAST in the chain:
//   - preserves the synthesized `Id` (universallyElidedTFFields is
//     empty on the AWS side; `id` is Optional+Computed → Configurable
//     → kept by the per-field check), AND
//   - elides the computed-only `Arn`.
//
// This is the canonical recipe for any new CC-routed AWS type that
// gates retirement of a hand-rolled enricher on decision-#5 parity.
func TestStripComputedOnlyForType_ComposesAfterRenameAndSynth(t *testing.T) {
	t.Parallel()
	n := chain(
		renameField("LogGroupName", "Name"),
		synthIDFromField("Name"),
		trimARNStar("Arn"),
		stripComputedOnlyForType("aws_cloudwatch_log_group"),
	)
	// CFN payload: LogGroupName lands as Name; synthIDFromField copies
	// it into Id; trimARNStar drops the :* wildcard; stripComputedOnly
	// elides `Arn` (Computed-only in the schema). Id survives because
	// `id` is NOT in the AWS universallyElidedTFFields list and its
	// schema is Optional+Computed (Configurable → kept).
	in := json.RawMessage(`{"LogGroupName":"/aws/x","Arn":"arn:aws:logs:us-east-1:1:log-group:/aws/x:*","RetentionInDays":30}`)
	out, err := n(in)
	require.NoError(t, err)
	m := asMap(t, out)
	assert.Equal(t, "/aws/x", m["Name"])
	assert.Equal(t, "/aws/x", m["Id"])
	assert.Equal(t, float64(30), m["RetentionInDays"])
	assert.NotContains(t, m, "Arn")
	assert.NotContains(t, m, "LogGroupName")
}

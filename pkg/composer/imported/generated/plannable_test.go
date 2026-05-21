package generated

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMissingRequiredAttrs_AllPresent covers the plannable case: an
// aws_iam_policy Attrs payload that carries its single Required argument
// (`policy`) reports no missing arguments.
func TestMissingRequiredAttrs_AllPresent(t *testing.T) {
	t.Parallel()

	attrs := AWSIAMPolicy{
		Name:   LiteralOf("io-policy"),
		Policy: LiteralOf(`{"Version":"2012-10-17"}`),
	}
	raw, err := json.Marshal(attrs)
	require.NoError(t, err)

	missing, err := MissingRequiredAttrs("aws_iam_policy", raw)
	require.NoError(t, err)
	assert.Empty(t, missing, "every Required argument is present")
}

// TestMissingRequiredAttrs_MissingPolicy covers the un-plannable case from
// the issue: an aws_iam_policy whose Attrs omit the Required `policy`
// argument is reported as missing exactly `policy`.
func TestMissingRequiredAttrs_MissingPolicy(t *testing.T) {
	t.Parallel()

	// Optional fields are present; the Required `policy` is omitted.
	raw := json.RawMessage(`{"name":{"literal":"io-policy"}}`)

	missing, err := MissingRequiredAttrs("aws_iam_policy", raw)
	require.NoError(t, err)
	assert.Equal(t, []string{"policy"}, missing)
}

// TestMissingRequiredAttrs_ExplicitNullIsMissing covers the tri-state edge:
// a Required argument decoded to an explicit-null *Value (`{"null":true}`)
// is reported missing — `terraform plan` rejects a required argument set to
// null exactly like an omitted one, so a non-nil-but-null pointer must NOT
// count as captured.
func TestMissingRequiredAttrs_ExplicitNullIsMissing(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{"name":{"literal":"io-policy"},"policy":{"null":true}}`)

	missing, err := MissingRequiredAttrs("aws_iam_policy", raw)
	require.NoError(t, err)
	assert.Equal(t, []string{"policy"}, missing, "explicit-null Required arg is not captured")
}

// TestMissingRequiredAttrs_ExprCounts confirms a Required argument carrying
// a Terraform expression (a wiring edge, not a literal) counts as captured —
// an expression resolves to a value at plan time.
func TestMissingRequiredAttrs_ExprCounts(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{"policy":{"expr":"data.aws_iam_policy_document.x.json"}}`)

	missing, err := MissingRequiredAttrs("aws_iam_policy", raw)
	require.NoError(t, err)
	assert.Empty(t, missing, "an expression-valued Required arg is plannable")
}

// TestMissingRequiredAttrs_EmptyRaw covers the "discovery captured nothing"
// case: an empty Attrs payload reports every Required argument as missing.
func TestMissingRequiredAttrs_EmptyRaw(t *testing.T) {
	t.Parallel()

	missing, err := MissingRequiredAttrs("aws_iam_policy", nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"policy"}, missing)

	missing, err = MissingRequiredAttrs("aws_iam_policy", json.RawMessage(``))
	require.NoError(t, err)
	assert.Equal(t, []string{"policy"}, missing)
}

// TestMissingRequiredAttrs_UndecodableRaw covers the malformed-payload case:
// a `raw` that cannot decode into the registered struct is treated as
// "discovery did not capture them" — every Required argument is reported
// missing, with a nil error (NOT a registry error).
func TestMissingRequiredAttrs_UndecodableRaw(t *testing.T) {
	t.Parallel()

	// A JSON array cannot decode into the struct.
	missing, err := MissingRequiredAttrs("aws_iam_policy", json.RawMessage(`[1,2,3]`))
	require.NoError(t, err)
	assert.Equal(t, []string{"policy"}, missing)
}

// TestMissingRequiredAttrs_UnregisteredType covers the only error path: a
// type absent from the registry returns an error so the caller knows
// plannability genuinely cannot be assessed.
func TestMissingRequiredAttrs_UnregisteredType(t *testing.T) {
	t.Parallel()

	missing, err := MissingRequiredAttrs("_definitely_not_registered", json.RawMessage(`{}`))
	require.Error(t, err)
	assert.Nil(t, missing)
	assert.Contains(t, err.Error(), "no registered type")
}

// TestMissingRequiredAttrs_ZeroRequiredFields covers a registered type with
// no Required arguments at all: it is always plannable, so the result is
// (nil, nil) regardless of the payload.
func TestMissingRequiredAttrs_ZeroRequiredFields(t *testing.T) {
	// The shared package-level registry is mutated here via Register;
	// t.Parallel is avoided so tests using Register don't interleave.
	const tfType = "_test_plannable_no_required"
	t.Cleanup(func() { unregisterForTest(tfType) })

	// regTestType's only field `name` is Optional — no Required arguments.
	Register(tfType, reflect.TypeFor[regTestType](), regTestTypeSchema, AWSProviderSource)

	missing, err := MissingRequiredAttrs(tfType, json.RawMessage(`{}`))
	require.NoError(t, err)
	assert.Nil(t, missing)

	missing, err = MissingRequiredAttrs(tfType, nil)
	require.NoError(t, err)
	assert.Nil(t, missing)
}

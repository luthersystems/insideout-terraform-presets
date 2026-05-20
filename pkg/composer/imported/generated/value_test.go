package generated

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValue_State(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		v    *Value[string]
		want ValueState
	}{
		{"absent_nil_pointer", nil, StateAbsent},
		{"absent_zero_value", &Value[string]{}, StateAbsent},
		{"null", NullOf[string](), StateNull},
		{"literal", LiteralOf("foo"), StateLiteral},
		{"expr", ExprOf[string]("aws_kms_key.main.arn"), StateExpr},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.v.State())
		})
	}
}

func TestValueJSON_Literal(t *testing.T) {
	t.Parallel()
	v := LiteralOf("orders-DLQ")
	b, err := json.Marshal(v)
	require.NoError(t, err)
	assert.Equal(t, `{"literal":"orders-DLQ"}`, string(b))

	var got Value[string]
	require.NoError(t, json.Unmarshal(b, &got))
	require.NotNil(t, got.Literal)
	assert.Equal(t, "orders-DLQ", *got.Literal)
	assert.Empty(t, got.Expr)
	assert.False(t, got.Null)
}

// TestValueJSON_CoercesStringEncodedScalars pins the tolerant literal
// decode: CloudFormation / Cloud Control serialize bool and numeric
// scalars as JSON strings ("true", "443", "1.5"), and a strict decode
// onto a Value[bool] / Value[int64] / Value[float64] field would
// hard-fail and abort the whole resource unmarshal. The retry against
// the unquoted bytes must land the value.
func TestValueJSON_CoercesStringEncodedScalars(t *testing.T) {
	t.Parallel()

	var b Value[bool]
	require.NoError(t, json.Unmarshal([]byte(`{"literal":"true"}`), &b),
		"string-encoded bool literal must coerce")
	require.NotNil(t, b.Literal)
	assert.True(t, *b.Literal)

	var bf Value[bool]
	require.NoError(t, json.Unmarshal([]byte(`{"literal":"false"}`), &bf))
	require.NotNil(t, bf.Literal)
	assert.False(t, *bf.Literal)

	var i Value[int64]
	require.NoError(t, json.Unmarshal([]byte(`{"literal":"443"}`), &i),
		"string-encoded int literal must coerce")
	require.NotNil(t, i.Literal)
	assert.Equal(t, int64(443), *i.Literal)

	var f Value[float64]
	require.NoError(t, json.Unmarshal([]byte(`{"literal":"1.5"}`), &f),
		"string-encoded float literal must coerce")
	require.NotNil(t, f.Literal)
	assert.InEpsilon(t, 1.5, *f.Literal, 1e-9)
}

// TestValueJSON_NativeScalarsUnaffected pins that the coercion is purely
// additive: a literal that already decodes strictly is never routed
// through the retry, and a native string literal is unchanged.
func TestValueJSON_NativeScalarsUnaffected(t *testing.T) {
	t.Parallel()

	var b Value[bool]
	require.NoError(t, json.Unmarshal([]byte(`{"literal":true}`), &b))
	require.NotNil(t, b.Literal)
	assert.True(t, *b.Literal)

	// A native string literal that happens to read like a bool must stay
	// the string "true", not be coerced.
	var s Value[string]
	require.NoError(t, json.Unmarshal([]byte(`{"literal":"true"}`), &s))
	require.NotNil(t, s.Literal)
	assert.Equal(t, "true", *s.Literal)
}

// TestValueJSON_UncoercibleStringStillErrors pins that a quoted string
// that is not a valid scalar of the target type still surfaces the
// original decode error rather than being silently swallowed.
func TestValueJSON_UncoercibleStringStillErrors(t *testing.T) {
	t.Parallel()

	var b Value[bool]
	err := json.Unmarshal([]byte(`{"literal":"hello"}`), &b)
	require.Error(t, err, `"hello" is not a bool — must still fail`)
	assert.Contains(t, err.Error(), "decoding literal")

	var i Value[int64]
	err = json.Unmarshal([]byte(`{"literal":""}`), &i)
	require.Error(t, err, "empty string is not an int — must still fail")
}

func TestValueJSON_Null(t *testing.T) {
	t.Parallel()
	v := NullOf[int64]()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	assert.Equal(t, `{"null":true}`, string(b))

	var got Value[int64]
	require.NoError(t, json.Unmarshal(b, &got))
	assert.True(t, got.Null)
	assert.Nil(t, got.Literal)
	assert.Empty(t, got.Expr)
}

func TestValueJSON_Expr(t *testing.T) {
	t.Parallel()
	v := ExprOf[string]("aws_kms_key.main.arn")
	b, err := json.Marshal(v)
	require.NoError(t, err)
	assert.Equal(t, `{"expr":"aws_kms_key.main.arn"}`, string(b))

	var got Value[string]
	require.NoError(t, json.Unmarshal(b, &got))
	assert.Equal(t, "aws_kms_key.main.arn", got.Expr)
	assert.Nil(t, got.Literal)
	assert.False(t, got.Null)
}

func TestValueJSON_AbsentRefusesMarshal(t *testing.T) {
	t.Parallel()
	// MarshalJSON refuses an absent value rather than emitting "{}". Callers
	// must use a nil *Value and rely on omitempty to omit the field.
	_, err := json.Marshal(Value[string]{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "absent")
}

func TestValueJSON_RejectsMultiSet_Unmarshal(t *testing.T) {
	t.Parallel()
	cases := []string{
		`{"null":true,"literal":"x"}`,
		`{"null":true,"expr":"a.b"}`,
		`{"literal":"x","expr":"a.b"}`,
		`{"null":true,"literal":"x","expr":"a.b"}`,
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			t.Parallel()
			var got Value[string]
			err := json.Unmarshal([]byte(in), &got)
			require.Error(t, err)
			assert.True(t,
				strings.Contains(err.Error(), "at most one") ||
					strings.Contains(err.Error(), "exactly one"),
				"unexpected error: %v", err)
		})
	}
}

func TestValueJSON_RejectsEmptyObject_Unmarshal(t *testing.T) {
	t.Parallel()
	var got Value[string]
	err := json.Unmarshal([]byte(`{}`), &got)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one")
}

func TestValueJSON_RejectsNullFalse(t *testing.T) {
	t.Parallel()
	var got Value[string]
	err := json.Unmarshal([]byte(`{"null":false}`), &got)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be true")
}

func TestValueJSON_OmitemptyOnContainingStruct(t *testing.T) {
	t.Parallel()
	// A nil *Value with omitempty must produce no output for the field.
	type wrapper struct {
		Name *Value[string] `json:"name,omitempty"`
	}
	b, err := json.Marshal(wrapper{})
	require.NoError(t, err)
	assert.Equal(t, `{}`, string(b))

	b, err = json.Marshal(wrapper{Name: LiteralOf("dlq")})
	require.NoError(t, err)
	assert.Equal(t, `{"name":{"literal":"dlq"}}`, string(b))
}

func TestValueJSON_RoundTrip_AllStates(t *testing.T) {
	t.Parallel()
	type wrapper struct {
		A *Value[string]  `json:"a,omitempty"`
		B *Value[bool]    `json:"b,omitempty"`
		C *Value[int64]   `json:"c,omitempty"`
		D *Value[float64] `json:"d,omitempty"`
		E *Value[string]  `json:"e,omitempty"`
		F *Value[string]  `json:"f,omitempty"`
	}
	in := wrapper{
		A: LiteralOf("hello"),
		B: LiteralOf(true),
		C: LiteralOf[int64](42),
		D: LiteralOf(3.14),
		E: NullOf[string](),
		F: ExprOf[string]("aws_kms_key.k.arn"),
	}
	b, err := json.Marshal(in)
	require.NoError(t, err)

	var got wrapper
	require.NoError(t, json.Unmarshal(b, &got))
	assert.Equal(t, in, got)
}

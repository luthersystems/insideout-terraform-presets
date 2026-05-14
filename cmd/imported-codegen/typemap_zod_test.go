package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zclconf/go-cty/cty"
)

func TestTSZodType_Scalars(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		ct   cty.Type
		want string
	}{
		{"string", cty.String, "expressionAware(z.string())"},
		// Integer-suffix attribute name does NOT switch the Zod
		// expression — TS doesn't distinguish int/float at this layer.
		{"number_int_suffix", cty.Number, "expressionAware(z.number())"},
		{"number_float", cty.Number, "expressionAware(z.number())"},
		{"bool", cty.Bool, "expressionAware(z.boolean())"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, _, _, err := TSZodType(tc.ct, "visibility_timeout_seconds", "Parent")
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestTSZodType_Collections(t *testing.T) {
	t.Parallel()

	got, _, _, err := TSZodType(cty.List(cty.String), "tags", "Parent")
	require.NoError(t, err)
	assert.Equal(t, "z.array(expressionAware(z.string()))", got)

	got, _, _, err = TSZodType(cty.Set(cty.String), "subnets", "Parent")
	require.NoError(t, err)
	assert.Equal(t, "z.array(expressionAware(z.string()))", got)

	got, _, _, err = TSZodType(cty.Map(cty.String), "tags", "Parent")
	require.NoError(t, err)
	assert.Equal(t, "z.record(z.string(), expressionAware(z.string()))", got)
}

func TestTSZodType_ObjectMaterializesNested(t *testing.T) {
	t.Parallel()
	objT := cty.Object(map[string]cty.Type{
		"key":   cty.String,
		"value": cty.Number,
	})
	got, nested, _, err := TSZodType(objT, "config", "AWSLambdaFunction")
	require.NoError(t, err)
	assert.Equal(t, "z.lazy(() => ZAWSLambdaFunctionConfig)", got)
	require.Len(t, nested, 1)
	assert.Equal(t, "AWSLambdaFunctionConfig", nested[0].GoName)
	require.Len(t, nested[0].Fields, 2)
	// Nested field GoType carries the Zod expression for the TS path.
	for _, f := range nested[0].Fields {
		switch f.TFName {
		case "key":
			assert.Equal(t, "expressionAware(z.string())", f.GoType)
		case "value":
			assert.Equal(t, "expressionAware(z.number())", f.GoType)
		}
	}
}

func TestTSZodType_TupleFallback(t *testing.T) {
	t.Parallel()
	got, _, _, err := TSZodType(cty.Tuple([]cty.Type{cty.String, cty.Number}), "mixed", "Parent")
	require.NoError(t, err)
	assert.Equal(t, "expressionAware(z.string())", got)
}

func TestReplacementToWire(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"Unknown":        "unknown",
		"Never":          "never",
		"MayReplace":     "may_replace",
		"AlwaysReplace":  "always_replace",
		"NotAReal Value": "",
	}
	for in, want := range cases {
		assert.Equal(t, want, replacementToWire(in), "replacementToWire(%q)", in)
	}
}

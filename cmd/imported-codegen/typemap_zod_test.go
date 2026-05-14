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
		name     string
		ct       cty.Type
		attrName string
		want     string
	}{
		{"string", cty.String, "name", "expressionAware(z.string())"},
		{"bool", cty.Bool, "fifo_queue", "expressionAware(z.boolean())"},
		{"number", cty.Number, "latitude", "expressionAware(z.number())"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, _, _, err := TSZodType(tc.ct, tc.attrName, "Parent")
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestTSZodType_NumberIgnoresIntegerHeuristic pins the intentional
// divergence from the Go emitter: GoFieldType maps cty.Number to
// int64 vs float64 based on isIntegerField(attrName); TSZodType always
// maps to z.number() because TS has no native int/float distinction at
// this layer.
func TestTSZodType_NumberIgnoresIntegerHeuristic(t *testing.T) {
	t.Parallel()
	for _, attr := range []string{
		"visibility_timeout_seconds", // suffix _seconds → Go emits int64
		"memory_size",                // exact match → Go emits int64
		"max_session_duration",       // prefix max_ → Go emits int64
		"latitude",                   // not matched → Go emits float64
	} {
		got, _, _, err := TSZodType(cty.Number, attr, "Parent")
		require.NoError(t, err)
		assert.Equal(t, "expressionAware(z.number())", got, "TS must not honor Go int/float heuristic for %q", attr)
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
		"Unknown":       "unknown",
		"Never":         "never",
		"MayReplace":    "may_replace",
		"AlwaysReplace": "always_replace",
	}
	for in, want := range cases {
		assert.Equal(t, want, replacementToWire(in), "replacementToWire(%q)", in)
	}
}

// TestReplacementToWire_UnknownPanics pins the fail-fast guard: a
// silent fallback would let a new ReplacementBehavior added to
// generated/schema.go compile on the Go emitter but silently drop the
// field on the TS emitter, breaking the issue #400 byte-for-byte
// parity contract with no test signal.
func TestReplacementToWire_UnknownPanics(t *testing.T) {
	t.Parallel()
	defer func() {
		r := recover()
		require.NotNil(t, r, "replacementToWire must panic on unknown suffix")
		msg, ok := r.(string)
		require.Truef(t, ok, "panic value should be string, got %T: %v", r, r)
		for _, want := range []string{"Sometimes", "replacementToWire"} {
			assert.Containsf(t, msg, want, "panic message %q must contain %q", msg, want)
		}
	}()
	_ = replacementToWire("Sometimes")
}

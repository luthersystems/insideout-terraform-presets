package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zclconf/go-cty/cty"
)

func TestGoFieldType_Scalars(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		ct       cty.Type
		attrName string
		want     string
	}{
		{"string", cty.String, "name", "*Value[string]"},
		{"bool", cty.Bool, "fifo_queue", "*Value[bool]"},
		{"number_default_float", cty.Number, "latitude", "*Value[float]"},
		{"number_int_via_suffix", cty.Number, "visibility_timeout_seconds", "*Value[int64]"},
		{"number_int_exact", cty.Number, "memory_size", "*Value[int64]"},
		{"number_int_max_prefix", cty.Number, "max_session_duration", "*Value[int64]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, _, _, err := GoFieldType(tc.ct, tc.attrName, "Parent")
			require.NoError(t, err)
			// The number_default_float case actually produces *Value[float64];
			// adjust the expectation.
			want := tc.want
			if want == "*Value[float]" {
				want = "*Value[float64]"
			}
			assert.Equal(t, want, got)
		})
	}
}

func TestGoFieldType_Collections(t *testing.T) {
	t.Parallel()
	// list of strings
	got, _, _, err := GoFieldType(cty.List(cty.String), "tags", "Parent")
	require.NoError(t, err)
	assert.Equal(t, "[]*Value[string]", got)

	// set of strings
	got, _, _, err = GoFieldType(cty.Set(cty.String), "subnets", "Parent")
	require.NoError(t, err)
	assert.Equal(t, "[]*Value[string]", got)

	// map of strings
	got, _, _, err = GoFieldType(cty.Map(cty.String), "tags", "Parent")
	require.NoError(t, err)
	assert.Equal(t, "map[string]*Value[string]", got)
}

func TestGoFieldType_ObjectMaterializesNested(t *testing.T) {
	t.Parallel()
	objT := cty.Object(map[string]cty.Type{
		"key":   cty.String,
		"value": cty.Number,
	})
	got, nested, _, err := GoFieldType(objT, "config", "AWSLambdaFunction")
	require.NoError(t, err)
	assert.Equal(t, "*AWSLambdaFunctionConfig", got)
	require.Len(t, nested, 1)
	assert.Equal(t, "AWSLambdaFunctionConfig", nested[0].GoName)
	require.Len(t, nested[0].Fields, 2)
}

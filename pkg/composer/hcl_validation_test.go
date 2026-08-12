package composer

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zclconf/go-cty/cty"
)

// Regression: ctyValueForType had no map case, so any object-shaped value —
// aws_backups.default_rule was the one that hit it in production — fell to
// "unsupported Go value type map[string]interface {}" and validate flagged a
// perfectly valid stack as invalid_type, failing the compose at deploy start.
func TestCtyValueForType_Map(t *testing.T) {
	t.Parallel()

	t.Run("default_rule shape converts", func(t *testing.T) {
		// The exact shape the mapper emits for aws_backups.default_rule.
		v := map[string]any{
			"schedule_expression":     "cron(0 0 * * ? *)",
			"retention_days":          7,
			"cold_storage_after_days": 0,
		}
		got, err := ctyValueForType(v, cty.DynamicPseudoType)
		require.NoError(t, err)
		require.True(t, got.Type().IsObjectType())
		require.Equal(t, "cron(0 0 * * ? *)", got.GetAttr("schedule_expression").AsString())
	})

	t.Run("nested maps and lists recurse", func(t *testing.T) {
		v := map[string]any{
			"selection": map[string]any{
				"resource_arns": []any{"arn:aws:s3:::b"},
			},
		}
		got, err := ctyValueForType(v, cty.DynamicPseudoType)
		require.NoError(t, err)
		sel := got.GetAttr("selection")
		require.True(t, sel.Type().IsObjectType())
		require.Equal(t, "arn:aws:s3:::b", sel.GetAttr("resource_arns").Index(cty.NumberIntVal(0)).AsString())
	})

	t.Run("empty map honors a map target", func(t *testing.T) {
		got, err := ctyValueForType(map[string]any{}, cty.Map(cty.String))
		require.NoError(t, err)
		require.True(t, got.Type().IsMapType())
		require.True(t, got.LengthInt() == 0)

		got, err = ctyValueForType(map[string]any{}, cty.DynamicPseudoType)
		require.NoError(t, err)
		require.True(t, got.Type().IsObjectType())
	})

	t.Run("unsupported element type still errors", func(t *testing.T) {
		_, err := ctyValueForType(map[string]any{"ch": make(chan int)}, cty.DynamicPseudoType)
		require.Error(t, err)
	})
}

// The map case above fixed the shape that reached production, but the switch
// was still type-by-type: map[string]string, []map[string]any, []bool, int32
// and friends each remained one mapper edit away from the same
// "unsupported Go value type" deploy-start 500. These assert the reflect-kind
// fallback closes the whole class, not just the instance.
func TestCtyValueForType_ReflectFallbackShapes(t *testing.T) {
	t.Parallel()

	num := 7

	t.Run("typed maps convert", func(t *testing.T) {
		// The shape a `tags` / `labels` variable would arrive as.
		got, err := ctyValueForType(map[string]string{"Project": "io-abc"}, cty.DynamicPseudoType)
		require.NoError(t, err)
		require.Equal(t, "io-abc", got.GetAttr("Project").AsString())

		got, err = ctyValueForType(map[string]bool{"enabled": true}, cty.DynamicPseudoType)
		require.NoError(t, err)
		require.True(t, got.GetAttr("enabled").True())

		got, err = ctyValueForType(map[string]int{"days": 30}, cty.DynamicPseudoType)
		require.NoError(t, err)
		require.Equal(t, "30", got.GetAttr("days").AsBigFloat().Text('f', -1))
	})

	t.Run("list of objects converts", func(t *testing.T) {
		// e.g. per-service backup rule overrides.
		got, err := ctyValueForType([]map[string]any{{"retention_days": 7}}, cty.DynamicPseudoType)
		require.NoError(t, err)
		require.True(t, got.Type().IsTupleType())
		require.Equal(t, "7",
			got.Index(cty.NumberIntVal(0)).GetAttr("retention_days").AsBigFloat().Text('f', -1))
	})

	t.Run("remaining scalar and slice kinds convert", func(t *testing.T) {
		for name, v := range map[string]any{
			"[]bool":    []bool{true},
			"[]float64": []float64{1.5},
			"int32":     int32(3),
			"float32":   float32(1.5),
			"uint":      uint(9),
			"pointer":   &num,
		} {
			_, err := ctyValueForType(v, cty.DynamicPseudoType)
			require.NoErrorf(t, err, "%s should convert", name)
		}
	})

	t.Run("empty typed collections honor the target", func(t *testing.T) {
		got, err := ctyValueForType(map[string]string{}, cty.Map(cty.String))
		require.NoError(t, err)
		require.True(t, got.Type().IsMapType())

		got, err = ctyValueForType([]bool{}, cty.List(cty.Bool))
		require.NoError(t, err)
		require.True(t, got.Type().IsListType())
	})

	t.Run("kinds with no HCL analogue still error", func(t *testing.T) {
		// The fallback must not silently coerce genuinely unconvertible
		// values — that would trade a loud validation failure for a
		// confusing terraform plan error further downstream.
		for name, v := range map[string]any{
			"struct":          struct{ A int }{1},
			"channel":         make(chan int),
			"non-string keys": map[int]string{1: "a"},
		} {
			_, err := ctyValueForType(v, cty.DynamicPseudoType)
			require.Errorf(t, err, "%s should error", name)
		}
	})
}

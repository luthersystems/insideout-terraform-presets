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

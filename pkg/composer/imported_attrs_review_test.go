package composer

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/policy"
)

const diffSecretSentinel = "SECRET-must-not-leak"

func typedLambda(t *testing.T, password string) imported.ImportedResource {
	t.Helper()
	raw := json.RawMessage(`{
		"function_name": {"literal": "api"},
		"environment": [{"variables": {"DB_PASSWORD": {"literal": "` + password + `"}, "STAGE": {"literal": "dev"}}}]
	}`)
	return imported.ImportedResource{
		Identity: imported.ResourceIdentity{Address: "aws_lambda_function.api", Type: "aws_lambda_function", Cloud: "aws"},
		Tier:     imported.TierImportedFlat,
		Attrs:    raw,
	}
}

func requireNoSecretInDiffs(t *testing.T, diffs []ResourceDiff, secret string) {
	t.Helper()
	b, err := json.Marshal(diffs)
	require.NoError(t, err)
	require.NotContains(t, string(b), secret, "diff leaked a sensitive nested value: %s", b)
}

// TestDiffImportedResources_RedactsNestedSensitive pins that the diff applies
// policy at nested paths: aws_lambda_function environment.variables is
// Sensitive, so its values must never reach the diff, on added, removed or
// modified resources.
func TestDiffImportedResources_RedactsNestedSensitive(t *testing.T) {
	t.Parallel()
	oldIR := typedLambda(t, diffSecretSentinel+"-old")
	newIR := typedLambda(t, diffSecretSentinel+"-new")

	requireNoSecretInDiffs(t, DiffImportedResources(nil, []imported.ImportedResource{newIR}), diffSecretSentinel)
	requireNoSecretInDiffs(t, DiffImportedResources([]imported.ImportedResource{oldIR}, nil), diffSecretSentinel)

	// environment.variables is also Hidden, so a change to it alone is no
	// visible change at all.
	modified := DiffImportedResources([]imported.ImportedResource{oldIR}, []imported.ImportedResource{newIR})
	requireNoSecretInDiffs(t, modified, diffSecretSentinel)
	require.Empty(t, modified)
}

// TestDiffAttributeMaps_NestedPolicyGuard walks every curated policy entry
// with a nested path that is Hidden or must be redacted, puts a sentinel at
// that path, and checks the diff never shows it. A new nested secret in any
// policy map is covered without a new test.
func TestDiffAttributeMaps_NestedPolicyGuard(t *testing.T) {
	t.Parallel()
	checked := 0
	for _, tfType := range policy.RegisteredTypes() {
		polMap, _ := policy.Lookup(tfType)
		goType, _, ok := generated.Lookup(tfType)
		if !ok {
			continue
		}
		paths := make([]string, 0, len(polMap))
		for path := range polMap {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		for _, path := range paths {
			entry := polMap[path]
			if !strings.Contains(path, ".") || strings.Contains(path, "[") {
				continue
			}
			if _, isProjection := policy.LookupJSONProjection(tfType, path); isProjection {
				continue
			}
			if entry.Visibility != policy.VisibilityHidden && !redactionRequired(entry) {
				continue
			}
			oldMap, ok := plainAtPath(goType, strings.Split(path, "."), diffSecretSentinel+"-old")
			if !ok {
				continue
			}
			newMap, _ := plainAtPath(goType, strings.Split(path, "."), diffSecretSentinel+"-new")
			checked++
			for name, pair := range map[string][2]map[string]any{
				"added":    {nil, newMap},
				"removed":  {oldMap, nil},
				"modified": {oldMap, newMap},
			} {
				changes := diffAttributeMaps(tfType, pair[0], pair[1])
				b, err := json.Marshal(changes)
				require.NoError(t, err)
				require.NotContainsf(t, string(b), diffSecretSentinel, "%s %s (%s): diff leaked the value: %s", tfType, path, name, b)
			}
		}
	}
	require.Positive(t, checked, "the guard must check at least one nested entry")
}

// plainAtPath builds the plain attribute map importedDesiredAttrs gives for
// a value at the dotted path of goType: a nested block list becomes []any of
// maps, a singleton block a map.
func plainAtPath(goType reflect.Type, segs []string, leaf string) (map[string]any, bool) {
	for goType.Kind() == reflect.Pointer {
		goType = goType.Elem()
	}
	if goType.Kind() != reflect.Struct {
		return nil, false
	}
	for i := 0; i < goType.NumField(); i++ {
		fld := goType.Field(i)
		name, _, _ := strings.Cut(fld.Tag.Get("tf"), ",")
		if name != segs[0] {
			continue
		}
		v, ok := plainFieldValue(fld.Type, segs[1:], leaf)
		if !ok {
			return nil, false
		}
		return map[string]any{name: v}, true
	}
	return nil, false
}

func plainFieldValue(t reflect.Type, rest []string, leaf string) (any, bool) {
	switch {
	case t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Struct && t.Elem().Name() != "" && !strings.HasPrefix(t.Elem().Name(), "Value["):
		if len(rest) == 0 {
			return []any{map[string]any{"x": leaf}}, true
		}
		m, ok := plainAtPath(t.Elem(), rest, leaf)
		return []any{m}, ok
	case t.Kind() == reflect.Pointer && t.Elem().Kind() == reflect.Struct && !strings.HasPrefix(t.Elem().Name(), "Value["):
		if len(rest) == 0 {
			return map[string]any{"x": leaf}, true
		}
		return plainAtPath(t.Elem(), rest, leaf)
	case len(rest) > 0:
		return nil, false
	case t.Kind() == reflect.Map:
		return map[string]any{"k": leaf}, true
	case t.Kind() == reflect.Slice:
		return []any{leaf}, true
	}
	return leaf, true
}

// TestDiffImportedResources_LegacyToTypedNormalized pins that moving a row
// from Attributes to typed Attrs with the same values shows no diff, when
// typed Attrs keeps jsonencode() as an expression, carries a nested
// reference, and has nested blocks the legacy map never stored.
func TestDiffImportedResources_LegacyToTypedNormalized(t *testing.T) {
	t.Parallel()
	legacy := imported.ImportedResource{
		Identity: imported.ResourceIdentity{Address: "aws_iam_role.r", Type: "aws_iam_role", Cloud: "aws"},
		Tier:     imported.TierImportedFlat,
		Attributes: map[string]any{
			"name":               "r",
			"assume_role_policy": `{"Statement":[{"Action":"sts:AssumeRole"}],"Version":"2012-10-17"}`,
		},
	}
	typed := legacy
	typed.Attributes = nil
	typed.Attrs = json.RawMessage(`{
		"name": {"literal": "r"},
		"assume_role_policy": {"expr": "jsonencode({\n  Version = \"2012-10-17\"\n  Statement = [{ Action = \"sts:AssumeRole\" }]\n})"},
		"inline_policy": [{"name": {"literal": "p"}, "policy": {"expr": "aws_iam_policy.p.policy"}}]
	}`)
	require.Empty(t, DiffImportedResources([]imported.ImportedResource{legacy}, []imported.ImportedResource{typed}))
}

// TestDiffImportedResources_NestedRawExprAsText pins that a nested
// expression renders as its source text, not {"Expr":...}.
func TestDiffImportedResources_NestedRawExprAsText(t *testing.T) {
	t.Parallel()
	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{Address: "aws_iam_role.r", Type: "aws_iam_role", Cloud: "aws"},
		Tier:     imported.TierImportedFlat,
		Attrs:    json.RawMessage(`{"inline_policy": [{"name": {"literal": "p"}, "policy": {"expr": "aws_iam_policy.p.policy"}}]}`),
	}
	b, err := json.Marshal(DiffImportedResources(nil, []imported.ImportedResource{ir}))
	require.NoError(t, err)
	require.NotContains(t, string(b), `Expr`)
	require.Contains(t, string(b), `aws_iam_policy.p.policy`)
}

func badAttrsSQS() imported.ImportedResource {
	return imported.ImportedResource{
		Identity:   imported.ResourceIdentity{Address: "aws_sqs_queue.q", Type: "aws_sqs_queue", Cloud: "aws"},
		Tier:       imported.TierImportedFlat,
		Attributes: map[string]any{"visibility_timeout_seconds": float64(30)},
		Attrs:      json.RawMessage(`{"visibility_timeout_seconds": "not-an-envelope"}`),
		FieldEdits: map[string]imported.FieldEdit{
			"visibility_timeout_seconds": {Source: imported.SourceAgent, EditedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), OldValue: float64(30), NewValue: float64(60)},
		},
	}
}

// TestDiffImportedResources_ReportsDecodeError pins that Attrs that do not
// decode surface as an error on the diff instead of a silent fallback.
func TestDiffImportedResources_ReportsDecodeError(t *testing.T) {
	t.Parallel()
	b, err := json.Marshal(DiffImportedResources(nil, []imported.ImportedResource{badAttrsSQS()}))
	require.NoError(t, err)
	require.Contains(t, string(b), `"error":`)
}

// TestValidateImportedResourceAuthorization_ReportsDecodeError pins that the
// re-import conflict check reports undecodable Attrs instead of reading the
// legacy map.
func TestValidateImportedResourceAuthorization_ReportsDecodeError(t *testing.T) {
	t.Parallel()
	issues := ValidateImportedResourceAuthorization("aws", []imported.ImportedResource{badAttrsSQS()})
	require.True(t, hasIssueCode(issues, "imported_resource_decode_failed"), "issues: %+v", issues)
}

// TestValidateCrossTierWiring_ReportsDecodeError pins that the wiring graph
// reports undecodable Attrs instead of treating the row as empty.
func TestValidateCrossTierWiring_ReportsDecodeError(t *testing.T) {
	t.Parallel()
	issues := ValidateCrossTierWiring(nil, []imported.ImportedResource{badAttrsSQS()})
	require.True(t, hasIssueCode(issues, "imported_resource_decode_failed"), "issues: %+v", issues)
}

func hasIssueCode(issues []ValidationIssue, code string) bool {
	for _, iss := range issues {
		if iss.Code == code {
			return true
		}
	}
	return false
}

// TestEvaluateReimportConflict_MergesLegacyKeys pins that a row with both
// Attrs and Attributes (an old row the backfill added Attrs to) still reads
// the keys only Attributes has: Attrs keys win, Attributes fills the rest.
func TestEvaluateReimportConflict_MergesLegacyKeys(t *testing.T) {
	t.Parallel()
	ir := typedSQS(t, "q", 30, "")
	ir.Attributes = map[string]any{"delay_seconds": float64(5), "visibility_timeout_seconds": float64(99)}
	edit := imported.FieldEdit{Source: imported.SourceAgent, EditedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), NewValue: float64(10)}

	_, conflict := evaluateReimportConflict("f", "delay_seconds", edit, ir)
	require.True(t, conflict, "delay_seconds lives only in Attributes (5), the edit wants 10")

	edit.NewValue = float64(30)
	_, conflict = evaluateReimportConflict("f", "visibility_timeout_seconds", edit, ir)
	require.False(t, conflict, "Attrs (30) wins over Attributes (99)")
}

// TestEvaluateReimportConflict_JSONEncodeExpr pins that a jsonencode() of
// constants compares by its evaluated value with a JSON string edit.
func TestEvaluateReimportConflict_JSONEncodeExpr(t *testing.T) {
	t.Parallel()
	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{Address: "aws_sqs_queue.q", Type: "aws_sqs_queue", Cloud: "aws"},
		Tier:     imported.TierImportedFlat,
		Attrs:    json.RawMessage(`{"redrive_policy": {"expr": "jsonencode({ maxReceiveCount = 5 })"}}`),
	}
	edit := imported.FieldEdit{Source: imported.SourceAgent, EditedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), NewValue: `{ "maxReceiveCount": 5 }`}
	_, conflict := evaluateReimportConflict("f", "redrive_policy", edit, ir)
	require.False(t, conflict, "same JSON value")

	edit.NewValue = `{"maxReceiveCount":6}`
	_, conflict = evaluateReimportConflict("f", "redrive_policy", edit, ir)
	require.True(t, conflict, "different JSON value")
}

// TestEvaluateReimportConflict_UnevaluableExprByText pins that an expression
// that cannot be evaluated (it reads another resource) is compared by text.
func TestEvaluateReimportConflict_UnevaluableExprByText(t *testing.T) {
	t.Parallel()
	ir := typedSQS(t, "q", 30, "aws_kms_key.k.arn")
	edit := imported.FieldEdit{Source: imported.SourceAgent, EditedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), NewValue: "aws_kms_key.k.arn"}
	_, conflict := evaluateReimportConflict("f", "kms_master_key_id", edit, ir)
	require.False(t, conflict)
	edit.NewValue = "aws_kms_key.other.arn"
	_, conflict = evaluateReimportConflict("f", "kms_master_key_id", edit, ir)
	require.True(t, conflict)
}

// TestExtractImportedEdges_Deduplicates pins that one consumer input that
// names the same target twice yields one edge.
func TestExtractImportedEdges_Deduplicates(t *testing.T) {
	t.Parallel()
	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{Address: "aws_sqs_queue.q", Type: "aws_sqs_queue"},
		Tier:     imported.TierImportedFlat,
		Attrs:    json.RawMessage(`{"policy": {"expr": "join(\"\", [aws_kms_key.k.arn, aws_kms_key.k.arn])"}}`),
	}
	edges := extractImportedEdges([]imported.ImportedResource{ir})
	require.Len(t, edges, 1, "edges: %+v", edges)
}

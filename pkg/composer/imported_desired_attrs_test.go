package composer

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// typedSQS returns an aws_sqs_queue that carries only typed Attrs, the
// shape reverse import writes once the legacy Attributes map is gone (#921).
func typedSQS(t *testing.T, name string, visibility int64, kmsExpr string) imported.ImportedResource {
	t.Helper()
	q := generated.AWSSQSQueue{
		Name:                     generated.LiteralOf(name),
		VisibilityTimeoutSeconds: generated.LiteralOf(visibility),
	}
	if kmsExpr != "" {
		q.KMSMasterKeyID = generated.ExprOf[string](kmsExpr)
	}
	raw, err := json.Marshal(q)
	require.NoError(t, err)
	return imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Address: "aws_sqs_queue." + name,
			Type:    "aws_sqs_queue",
			Cloud:   "aws",
		},
		Tier:  imported.TierImportedFlat,
		Attrs: raw,
	}
}

func TestImportedDesiredAttrs_UnwrapsTypedAttrs(t *testing.T) {
	t.Parallel()
	got := importedDesiredAttrs(typedSQS(t, "orders", 30, "aws_kms_key.queues.arn"))
	require.Equal(t, map[string]any{
		"name":                       "orders",
		"visibility_timeout_seconds": float64(30),
		"kms_master_key_id":          RawExpr{Expr: "aws_kms_key.queues.arn"},
	}, got)
}

func TestImportedDesiredAttrs_UnwrapsNestedBlocksAndNull(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{
		"name": {"null": true},
		"tags": {"Env": {"literal": "dev"}},
		"timeouts": {"create": {"literal": "5m"}}
	}`)
	ir := imported.ImportedResource{
		Identity: imported.ResourceIdentity{Address: "aws_sqs_queue.q", Type: "aws_sqs_queue"},
		Attrs:    raw,
	}
	require.Equal(t, map[string]any{
		"name":     nil,
		"tags":     map[string]any{"Env": "dev"},
		"timeouts": map[string]any{"create": "5m"},
	}, importedDesiredAttrs(ir))
}

func TestImportedDesiredAttrs_FallsBackToLegacyAttributes(t *testing.T) {
	t.Parallel()
	legacy := sampleSQS("q", 30)
	require.Equal(t, legacy.Attributes, importedDesiredAttrs(legacy))

	// Attrs that do not decode against the model fall back too.
	legacy.Attrs = json.RawMessage(`{"name": "not-an-envelope"}`)
	require.Equal(t, legacy.Attributes, importedDesiredAttrs(legacy))
}

// TestDiffImportedResources_TypedAttrsOnly pins that the diff reads typed
// Attrs: before #921 it read only Attributes, so a resource with typed
// Attrs and no Attributes diffed as having no field changes.
func TestDiffImportedResources_TypedAttrsOnly(t *testing.T) {
	t.Parallel()

	added := DiffImportedResources(nil, []imported.ImportedResource{typedSQS(t, "q", 30, "")})
	require.Len(t, added, 1)
	require.Equal(t, ResourceActionAdded, added[0].Action)
	require.Contains(t, fieldPaths(added[0].Changes), "visibility_timeout_seconds")

	modified := DiffImportedResources(
		[]imported.ImportedResource{typedSQS(t, "q", 30, "")},
		[]imported.ImportedResource{typedSQS(t, "q", 60, "")},
	)
	require.Len(t, modified, 1)
	require.Equal(t, ResourceActionModified, modified[0].Action)
	require.Len(t, modified[0].Changes, 1)
	require.Equal(t, []ResourceFieldDiff{{
		Path: "visibility_timeout_seconds",
		From: "30",
		To:   "60",
	}}, stripPolicy(modified[0].Changes, "visibility_timeout_seconds"))

	require.Empty(t, DiffImportedResources(
		[]imported.ImportedResource{typedSQS(t, "q", 30, "")},
		[]imported.ImportedResource{typedSQS(t, "q", 30, "")},
	))
}

// TestDiffImportedResources_LegacyToTypedSameValues pins that a row moved
// from Attributes to typed Attrs with the same values shows no diff.
func TestDiffImportedResources_LegacyToTypedSameValues(t *testing.T) {
	t.Parallel()
	legacy := imported.ImportedResource{
		Identity: imported.ResourceIdentity{Address: "aws_sqs_queue.q", Type: "aws_sqs_queue", Cloud: "aws"},
		Tier:     imported.TierImportedFlat,
		Attributes: map[string]any{
			"name":                       "q",
			"visibility_timeout_seconds": float64(30),
			"kms_master_key_id":          "aws_kms_key.queues.arn",
		},
	}
	require.Empty(t, DiffImportedResources(
		[]imported.ImportedResource{legacy},
		[]imported.ImportedResource{typedSQS(t, "q", 30, "aws_kms_key.queues.arn")},
	))
}

func fieldPaths(changes []ResourceFieldDiff) []string {
	out := make([]string, 0, len(changes))
	for _, c := range changes {
		out = append(out, c.Path)
	}
	return out
}

// stripPolicy keeps only the change at path, with its policy metadata
// cleared, so the test pins the values without restating the curated map.
func stripPolicy(changes []ResourceFieldDiff, path string) []ResourceFieldDiff {
	var out []ResourceFieldDiff
	for _, c := range changes {
		if c.Path == path {
			out = append(out, ResourceFieldDiff{Path: c.Path, From: c.From, To: c.To})
		}
	}
	return out
}

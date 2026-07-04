package composer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// TestEmitImportedTFWithIssues_SurfacesEmitFailure pins the fail-open fix
// (reliable #1922): a resource whose typed Attrs fail to decode at emit time
// must surface an imported_resource_emit_failed issue instead of silently
// vanishing from /imported.tf, and it must NOT take the rest of the batch down
// with it — a valid sibling resource still emits.
//
// The bad fixture gives aws_s3_bucket a numeric `bucket` literal where the
// generated model expects Value[string]; generated.UnmarshalAttrs rejects it
// inside emitImportedResourceBody, hitting the first (body-emit) continue site.
func TestEmitImportedTFWithIssues_SurfacesEmitFailure(t *testing.T) {
	t.Parallel()

	badAddr := "aws_s3_bucket.broken"
	goodAddr := "aws_sqs_queue.orders_dlq"

	bad := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:    "aws",
			Type:     "aws_s3_bucket",
			Address:  badAddr,
			Region:   "us-east-1",
			ImportID: "broken-bucket",
		},
		Tier: imported.TierImportedFlat,
		// bucket is Value[string]; a numeric literal fails UnmarshalAttrs.
		Attrs: []byte(`{"bucket": {"literal": 123}}`),
	}
	good := imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Cloud:    "aws",
			Type:     "aws_sqs_queue",
			Address:  goodAddr,
			Region:   "us-east-1",
			ImportID: "https://sqs.us-east-1.amazonaws.com/123/orders-DLQ",
		},
		Tier:  imported.TierImportedFlat,
		Attrs: []byte(`{"name":{"literal":"orders-DLQ"}}`),
	}

	out, used, issues := EmitImportedTFWithIssues("aws", []imported.ImportedResource{bad, good}, EmitImportedOpts{})

	// (a) exactly one emit-failure issue, naming the bad address.
	var emitFail []ValidationIssue
	for _, iss := range issues {
		if iss.Code == CodeImportedResourceEmitFailed {
			emitFail = append(emitFail, iss)
		}
	}
	require.Len(t, emitFail, 1, "expected exactly one imported_resource_emit_failed issue, got: %+v", issues)
	assert.Equal(t, "imported."+badAddr, emitFail[0].Field)
	assert.Equal(t, "aws_s3_bucket", emitFail[0].Value)
	assert.Contains(t, emitFail[0].Reason, "emit resource body:")

	// (b) the valid resource still emits.
	require.NotNil(t, out)
	s := string(out)
	assert.Contains(t, s, goodAddr, "valid resource must still emit:\n%s", s)
	assert.True(t, used["aws"])

	// (c) the bad address never appears in the output.
	assert.NotContains(t, s, badAddr, "failed resource must not leak into imported.tf:\n%s", s)
}

// TestComposeStack_SurfacesEmitFailureIssue pushes the same bad IR through the
// full compose path. See the in-body comment for the pre-validation-shadowing
// analysis: the decode failure is flagged twice (once by ValidateImportedResources
// pre-emit, once by the emit site), and dropUncomposable does NOT remove it
// (its `bucket` key is present, so it is not missing-required). Both codes fire.
func TestComposeStack_SurfacesEmitFailureIssue(t *testing.T) {
	t.Parallel()

	badAddr := "aws_s3_bucket.broken"
	c := newTestClient()
	res, err := c.ComposeStackWithIssues(ComposeStackOpts{
		Cloud:        "aws",
		SelectedKeys: []ComponentKey{KeyAWSVPC},
		Comps:        &Components{Cloud: "AWS"},
		Cfg:          &Config{},
		Project:      "demo",
		Region:       "us-east-1",
		Imported: []imported.ImportedResource{
			{
				Identity: imported.ResourceIdentity{
					Cloud:    "aws",
					Type:     "aws_s3_bucket",
					Address:  badAddr,
					Region:   "us-east-1",
					ImportID: "broken-bucket",
				},
				Tier:  imported.TierImportedFlat,
				Attrs: []byte(`{"bucket": {"literal": 123}}`),
			},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, res)

	var sawDecode, sawEmit bool
	for _, iss := range res.Issues {
		switch iss.Code {
		case "imported_resource_decode_failed":
			sawDecode = true
		case CodeImportedResourceEmitFailed:
			sawEmit = true
			assert.Equal(t, "imported."+badAddr, iss.Field)
		}
	}
	// Pre-validation shadows the decode error, but does not suppress the emit
	// site: the emit-failure issue still fires on the compose path.
	assert.True(t, sawDecode, "expected pre-validation to flag imported_resource_decode_failed; issues: %+v", res.Issues)
	assert.True(t, sawEmit, "expected the emit site to flag imported_resource_emit_failed; issues: %+v", res.Issues)

	// The bad resource must not have leaked into the emitted archive.
	assert.NotContains(t, string(res.Files["/imported.tf"]), badAddr)
}

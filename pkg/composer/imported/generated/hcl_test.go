package generated

import (
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// hclTestQueue is a tiny stand-in for a generated struct. Used to exercise
// the reflection HCL walker without depending on real provider schemas.
// Generated types follow this exact shape (private to tests so it does not
// pollute the public surface).
type hclTestQueue struct {
	Name                     *Value[string]            `tf:"name"`
	FifoQueue                *Value[bool]              `tf:"fifo_queue"`
	VisibilityTimeoutSeconds *Value[int64]             `tf:"visibility_timeout_seconds"`
	KMSMasterKeyID           *Value[string]            `tf:"kms_master_key_id"`
	Tags                     map[string]*Value[string] `tf:"tags"`
	RedrivePolicy            *Value[string]            `tf:"redrive_policy"`
}

type hclTestBucket struct {
	Name          *Value[string]     `tf:"name"`
	Versioning    *hclTestVersioning `tf:"versioning,block"`
	LifecycleRule []hclTestLifecycle `tf:"lifecycle_rule,blocks"`
}

type hclTestVersioning struct {
	Enabled *Value[bool] `tf:"enabled"`
}

type hclTestLifecycle struct {
	ID      *Value[string] `tf:"id"`
	Enabled *Value[bool]   `tf:"enabled"`
}

func TestUnmarshalHCL_Scalars(t *testing.T) {
	t.Parallel()
	src := []byte(`
name                        = "orders-DLQ"
fifo_queue                  = false
visibility_timeout_seconds  = 30
kms_master_key_id           = aws_kms_key.main.arn
redrive_policy              = null
tags = {
  Environment = "staging"
  Team        = "platform"
}
`)

	var q hclTestQueue
	require.NoError(t, parseAndUnmarshal(t, src, &q))

	require.NotNil(t, q.Name)
	require.NotNil(t, q.Name.Literal)
	assert.Equal(t, "orders-DLQ", *q.Name.Literal)

	require.NotNil(t, q.FifoQueue)
	require.NotNil(t, q.FifoQueue.Literal)
	assert.False(t, *q.FifoQueue.Literal)

	require.NotNil(t, q.VisibilityTimeoutSeconds)
	require.NotNil(t, q.VisibilityTimeoutSeconds.Literal)
	assert.Equal(t, int64(30), *q.VisibilityTimeoutSeconds.Literal)

	require.NotNil(t, q.KMSMasterKeyID)
	assert.Equal(t, "aws_kms_key.main.arn", q.KMSMasterKeyID.Expr,
		"reference expressions must round-trip as Expr, not as a literal")

	require.NotNil(t, q.RedrivePolicy)
	assert.True(t, q.RedrivePolicy.Null, "explicit null must round-trip as Null state")

	require.Len(t, q.Tags, 2)
	require.NotNil(t, q.Tags["Environment"].Literal)
	assert.Equal(t, "staging", *q.Tags["Environment"].Literal)
}

func TestMarshalHCL_Scalars(t *testing.T) {
	t.Parallel()
	q := hclTestQueue{
		Name:                     LiteralOf("orders-DLQ"),
		FifoQueue:                LiteralOf(false),
		VisibilityTimeoutSeconds: LiteralOf[int64](30),
		KMSMasterKeyID:           ExprOf[string]("aws_kms_key.main.arn"),
		RedrivePolicy:            NullOf[string](),
	}
	out, err := MarshalHCL(&q)
	require.NoError(t, err)

	s := string(out)
	assert.Contains(t, s, `name                       = "orders-DLQ"`)
	assert.Contains(t, s, `fifo_queue                 = false`)
	assert.Contains(t, s, `visibility_timeout_seconds = 30`)
	assert.Contains(t, s, `kms_master_key_id          = aws_kms_key.main.arn`)
	assert.Contains(t, s, `redrive_policy             = null`)
}

func TestRoundTrip_HCL_Scalars(t *testing.T) {
	t.Parallel()
	src := []byte(`name                       = "orders-DLQ"
fifo_queue                 = false
visibility_timeout_seconds = 30
kms_master_key_id          = aws_kms_key.main.arn
redrive_policy             = null
`)
	var q hclTestQueue
	require.NoError(t, parseAndUnmarshal(t, src, &q))

	out, err := MarshalHCL(&q)
	require.NoError(t, err)
	// hclwrite normalizes alignment; compare via decoded round-trip rather
	// than byte-equality on this fixture.
	var q2 hclTestQueue
	require.NoError(t, parseAndUnmarshal(t, out, &q2))
	assert.Equal(t, q, q2)
}

func TestUnmarshalHCL_NestedBlocks(t *testing.T) {
	t.Parallel()
	src := []byte(`
name = "assets"

versioning {
  enabled = true
}

lifecycle_rule {
  id      = "expire-old"
  enabled = true
}

lifecycle_rule {
  id      = "infrequent-access"
  enabled = false
}
`)
	var b hclTestBucket
	require.NoError(t, parseAndUnmarshal(t, src, &b))

	require.NotNil(t, b.Versioning)
	require.NotNil(t, b.Versioning.Enabled)
	require.NotNil(t, b.Versioning.Enabled.Literal)
	assert.True(t, *b.Versioning.Enabled.Literal)

	require.Len(t, b.LifecycleRule, 2)
	require.NotNil(t, b.LifecycleRule[0].ID)
	require.NotNil(t, b.LifecycleRule[0].ID.Literal)
	assert.Equal(t, "expire-old", *b.LifecycleRule[0].ID.Literal)
	require.NotNil(t, b.LifecycleRule[1].Enabled)
	require.NotNil(t, b.LifecycleRule[1].Enabled.Literal)
	assert.False(t, *b.LifecycleRule[1].Enabled.Literal)
}

func TestRoundTrip_HCL_NestedBlocks(t *testing.T) {
	t.Parallel()
	src := []byte(`name = "assets"

versioning {
  enabled = true
}

lifecycle_rule {
  id      = "expire-old"
  enabled = true
}
`)
	var b hclTestBucket
	require.NoError(t, parseAndUnmarshal(t, src, &b))

	out, err := MarshalHCL(&b)
	require.NoError(t, err)

	var b2 hclTestBucket
	require.NoError(t, parseAndUnmarshal(t, out, &b2))
	assert.Equal(t, b, b2)
}

func TestUnmarshalHCL_TolerantToUnknownAttributes(t *testing.T) {
	t.Parallel()
	// Newer providers sometimes add fields. The walker silently skips
	// unknown attributes/blocks rather than failing — older typed models
	// stay forward-compatible.
	src := []byte(`
name                = "orders-DLQ"
some_future_field   = "ignored"

future_block {
  whatever = 1
}
`)
	var q hclTestQueue
	require.NoError(t, parseAndUnmarshal(t, src, &q))
	require.NotNil(t, q.Name)
	require.NotNil(t, q.Name.Literal)
	assert.Equal(t, "orders-DLQ", *q.Name.Literal)
}

// parseAndUnmarshal is a small helper that wraps hclsyntax parsing so test
// bodies stay focused on the assertions.
func parseAndUnmarshal(t *testing.T, src []byte, into any) error {
	t.Helper()
	file, diags := hclsyntax.ParseConfig(src, "test.tf", hcl.Pos{Line: 1, Column: 1})
	require.False(t, diags.HasErrors(), "parse: %s", diags.Error())
	body, ok := file.Body.(*hclsyntax.Body)
	require.True(t, ok)
	return UnmarshalHCL(src, body, into)
}

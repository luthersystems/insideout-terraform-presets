package awsdiscover

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// TestStringifyValueStringFieldsForType pins the generic #646 fix:
// object- and array-valued CFN keys that land on a `*Value[string]`
// attribute of the registered Layer-1 struct are JSON-encoded to
// strings, while scalars, non-matching keys, and non-string-typed
// Value fields are left untouched.
func TestStringifyValueStringFieldsForType(t *testing.T) {
	t.Parallel()

	t.Run("object and array on Value[string] fields are stringified", func(t *testing.T) {
		t.Parallel()
		props := map[string]any{
			// redrive_policy is *Value[string] — object must be stringified.
			"RedrivePolicy": map[string]any{
				"deadLetterTargetArn": "arn:aws:sqs:us-east-1:123:dlq",
				"maxReceiveCount":     float64(5),
			},
			// redrive_allow_policy is *Value[string] — object (with a
			// nested array) must be stringified.
			"RedriveAllowPolicy": map[string]any{
				"redrivePermission": "byQueue",
				"sourceQueueArns":   []any{"arn:aws:sqs:us-east-1:123:src"},
			},
			// policy is *Value[string] — a bare top-level array must hit
			// the []any switch arm directly (not nested inside an object).
			"Policy": []any{"a", "b"},
			// name is *Value[string] but already a scalar — untouched.
			"QueueName": "demo",
			// delay_seconds is *Value[int64], not *Value[string] — an
			// object here is a genuine shape error this pass must NOT mask.
			"DelaySeconds": map[string]any{"unexpected": true},
		}
		got := stringifyValueStringFieldsForType("aws_sqs_queue", props)

		rp, ok := got["RedrivePolicy"].(string)
		require.True(t, ok, "RedrivePolicy must be a JSON string, got %T", got["RedrivePolicy"])
		var rpDoc map[string]any
		require.NoError(t, json.Unmarshal([]byte(rp), &rpDoc))
		assert.Equal(t, "arn:aws:sqs:us-east-1:123:dlq", rpDoc["deadLetterTargetArn"])

		rap, ok := got["RedriveAllowPolicy"].(string)
		require.True(t, ok, "RedriveAllowPolicy must be a JSON string, got %T", got["RedriveAllowPolicy"])
		var rapDoc map[string]any
		require.NoError(t, json.Unmarshal([]byte(rap), &rapDoc))
		assert.Equal(t, "byQueue", rapDoc["redrivePermission"])

		pol, ok := got["Policy"].(string)
		require.True(t, ok, "Policy (bare array) must be a JSON string, got %T", got["Policy"])
		assert.JSONEq(t, `["a","b"]`, pol)

		assert.Equal(t, "demo", got["QueueName"], "scalar value left untouched")
		assert.Equal(t, map[string]any{"unexpected": true}, got["DelaySeconds"],
			"object on a non-string Value field is left untouched, not masked")
	})

	t.Run("unregistered type passes through unchanged", func(t *testing.T) {
		t.Parallel()
		props := map[string]any{"RedrivePolicy": map[string]any{"x": 1}}
		got := stringifyValueStringFieldsForType("aws_not_a_real_type", props)
		assert.IsType(t, map[string]any{}, got["RedrivePolicy"], "no struct to reflect — pass through")
	})

	t.Run("nil map passes through", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, stringifyValueStringFieldsForType("aws_sqs_queue", nil))
	})
}

// TestCloudControlEnricher_Enrich_SQSRedrivePolicy is the end-to-end
// regression pin for #646. A CloudFormation AWS::SQS::Queue payload
// surfaces RedrivePolicy / RedriveAllowPolicy as nested JSON objects,
// but the Terraform aws_sqs_queue.redrive_policy / .redrive_allow_policy
// attributes are JSON-encoded *strings* (`*Value[string]` on the
// generated struct). Pre-fix, shapeCFNForLayer1 recursed the object and
// landed a literal-less envelope on the string field, which
// Value[T].UnmarshalJSON rejected ("at least one of null/literal/expr
// must be present"), aborting the whole UnmarshalAttrs and dropping
// every Attr. The generic stringify pass must bridge the gap so the
// enriched typed Attrs decode cleanly with the policies as JSON strings.
func TestCloudControlEnricher_Enrich_SQSRedrivePolicy(t *testing.T) {
	t.Parallel()
	fake := &fakeCCGet{
		props: `{
			"QueueName": "demo-queue",
			"Arn": "arn:aws:sqs:us-east-1:123456789012:demo-queue",
			"VisibilityTimeout": 30,
			"RedrivePolicy": {
				"deadLetterTargetArn": "arn:aws:sqs:us-east-1:123456789012:demo-dlq",
				"maxReceiveCount": 5
			},
			"RedriveAllowPolicy": {
				"redrivePermission": "byQueue",
				"sourceQueueArns": ["arn:aws:sqs:us-east-1:123456789012:src-queue"]
			},
			"Tags": [
				{"Key": "Project", "Value": "io-abc"}
			]
		}`,
	}
	// Pull the Normalizer from the production config so a registration
	// drift fails this test alongside the generic-pass regression.
	n := normalizerForCFNType(t, "AWS::SQS::Queue")
	enr := newCloudControlEnricherWithNormalizer("aws_sqs_queue", "AWS::SQS::Queue", fake.call, n)
	ir := &imported.ImportedResource{
		Identity: imported.ResourceIdentity{
			Type:     "aws_sqs_queue",
			ImportID: "https://sqs.us-east-1.amazonaws.com/123456789012/demo-queue",
		},
	}
	require.NoError(t, enr.Enrich(context.Background(), ir, EnrichClients{}))

	// Pre-fix this aborted with the Value-decode error and Attrs was nil.
	require.NotEmpty(t, ir.Attrs, "Attrs must not be dropped by the enricher")

	decoded, err := generated.UnmarshalAttrs("aws_sqs_queue", ir.Attrs)
	require.NoError(t, err)
	q, ok := decoded.(*generated.AWSSQSQueue)
	require.True(t, ok, "decoded type is %T", decoded)

	// redrive_policy carries a valid JSON string.
	require.NotNil(t, q.RedrivePolicy, "redrive_policy must be populated")
	require.NotNil(t, q.RedrivePolicy.Literal)
	var rp map[string]any
	require.NoError(t, json.Unmarshal([]byte(*q.RedrivePolicy.Literal), &rp),
		"redrive_policy must hold a valid JSON string")
	assert.Equal(t, "arn:aws:sqs:us-east-1:123456789012:demo-dlq", rp["deadLetterTargetArn"])

	// redrive_allow_policy carries a valid JSON string, nested array intact.
	require.NotNil(t, q.RedriveAllowPolicy, "redrive_allow_policy must be populated")
	require.NotNil(t, q.RedriveAllowPolicy.Literal)
	var rap map[string]any
	require.NoError(t, json.Unmarshal([]byte(*q.RedriveAllowPolicy.Literal), &rap),
		"redrive_allow_policy must hold a valid JSON string")
	assert.Equal(t, "byQueue", rap["redrivePermission"])

	// Sibling fields still flow through the per-type Normalizer + renamer.
	require.NotNil(t, q.Name)
	require.NotNil(t, q.Name.Literal)
	assert.Equal(t, "demo-queue", *q.Name.Literal)
	require.NotNil(t, q.VisibilityTimeoutSeconds)
	require.NotNil(t, q.VisibilityTimeoutSeconds.Literal)
	assert.Equal(t, int64(30), *q.VisibilityTimeoutSeconds.Literal)
	require.Contains(t, q.Tags, "Project")
	require.NotNil(t, q.Tags["Project"].Literal)
	assert.Equal(t, "io-abc", *q.Tags["Project"].Literal)
}

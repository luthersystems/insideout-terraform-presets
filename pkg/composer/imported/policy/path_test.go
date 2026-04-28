package policy

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	// Side-effect import: register the 10 generated Layer 1 types so
	// ResolvePath has something to walk.
	_ "github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

func TestResolvePath_Generated(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		tfType  string
		path    string
		wantErr bool
	}{
		// Top-level scalar leaves
		{"sqs name", "aws_sqs_queue", "name", false},
		{"sqs visibility timeout", "aws_sqs_queue", "visibility_timeout_seconds", false},
		{"sqs kms id", "aws_sqs_queue", "kms_master_key_id", false},
		// Map leaf
		{"sqs tags", "aws_sqs_queue", "tags", false},
		{"sqs tags with key", "aws_sqs_queue", `tags["Project"]`, false},
		// `,blocks` nested
		{"dynamodb sse kms", "aws_dynamodb_table", "server_side_encryption.kms_key_arn", false},
		{"dynamodb ttl enabled", "aws_dynamodb_table", "ttl.enabled", false},
		{"dynamodb pitr enabled", "aws_dynamodb_table", "point_in_time_recovery.enabled", false},
		{"dynamodb replica region", "aws_dynamodb_table", "replica.region_name", false},
		// `,blocks` deeply nested
		{"storage bucket lifecycle action type",
			"google_storage_bucket", "lifecycle_rule.action.type", false},
		{"secret replication user_managed cmek",
			"google_secret_manager_secret",
			"replication.user_managed.replicas.customer_managed_encryption.kms_key_name", false},
		// `,block` singleton (timeouts)
		{"dynamodb timeouts create", "aws_dynamodb_table", "timeouts.create", false},
		{"storage bucket timeouts read", "google_storage_bucket", "timeouts.read", false},
		// list-of-scalars
		{"lambda layers", "aws_lambda_function", "layers", false},
		{"lambda vpc subnet ids", "aws_lambda_function", "vpc_config.subnet_ids", false},
		// Map with bracket key
		{"lambda env var",
			"aws_lambda_function",
			`environment.variables["DATABASE_URL"]`, false},
		// Pubsub topic deep wiring
		{"pubsub topic kinesis role",
			"google_pubsub_topic",
			"ingestion_data_source_settings.aws_kinesis.aws_role_arn", false},
		// Negative cases
		{"unknown top-level", "aws_sqs_queue", "no_such_attr", true},
		{"unknown nested", "aws_dynamodb_table", "ttl.no_such", true},
		{"descended past leaf", "aws_sqs_queue", "name.extra", true},
		{"unknown tfType", "aws_no_such_resource", "name", true},
		{"empty path", "aws_sqs_queue", "", true},
		{"unbalanced bracket", "aws_sqs_queue", "tags[unclosed", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ResolvePath(tc.tfType, tc.path)
			if tc.wantErr {
				require.Error(t, err)
				assert.True(t, errors.Is(err, ErrNoSuchPath),
					"expected ErrNoSuchPath wrap, got: %v", err)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestResolvePath_JSONProjection(t *testing.T) {
	const tfType = "aws_sqs_queue"
	const path = "redrive_policy.deadLetterTargetArn"

	// Path won't resolve via Layer 1 walker because the JSON subpath
	// has no struct field; only a registered projection makes it valid.
	err := ResolvePath(tfType, path)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNoSuchPath))

	t.Cleanup(func() {
		projMu.Lock()
		defer projMu.Unlock()
		delete(projReg[tfType], path)
		if len(projReg[tfType]) == 0 {
			delete(projReg, tfType)
		}
	})
	RegisterJSONProjection(tfType, JSONProjection{Parent: "redrive_policy", Subpath: "deadLetterTargetArn"})

	assert.NoError(t, ResolvePath(tfType, path))
}

func TestRegisterJSONProjection_DuplicatePanics(t *testing.T) {
	const tfType = "policy_test_jsonproj_dup"
	const sub = "x"
	t.Cleanup(func() {
		projMu.Lock()
		defer projMu.Unlock()
		delete(projReg, tfType)
	})
	RegisterJSONProjection(tfType, JSONProjection{Parent: "p", Subpath: sub})
	assert.Panics(t, func() {
		RegisterJSONProjection(tfType, JSONProjection{Parent: "p", Subpath: sub})
	})
}

func TestRegisterJSONProjection_InvalidPanics(t *testing.T) {
	t.Parallel()
	assert.Panics(t, func() {
		RegisterJSONProjection("x", JSONProjection{Parent: "", Subpath: "y"})
	})
	assert.Panics(t, func() {
		RegisterJSONProjection("x", JSONProjection{Parent: "y", Subpath: ""})
	})
}

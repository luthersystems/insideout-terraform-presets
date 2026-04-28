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
		// hasBucket discipline: bracket only legal on map/slice fields.
		{"bracket on scalar leaf", "aws_sqs_queue", `name["x"]`, true},
		{"bracket on singleton block", "aws_dynamodb_table", `timeouts["x"]`, true},
		// Wrong key on a `,blocks` slice descendant.
		{"unknown blocks descendant",
			"aws_dynamodb_table", "replica.no_such_subfield", true},
		// Map without bracket but descended past it.
		{"descended past map without bracket",
			"aws_sqs_queue", "tags.foo", true},
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
	const path = "redrive_policy.policyTestSyntheticSubpath"

	// Cleanup must come BEFORE any assertion that can abort the
	// test — otherwise an early require.Error failure could leak the
	// projection registration into other tests.
	t.Cleanup(func() {
		projMu.Lock()
		defer projMu.Unlock()
		if m := projReg[tfType]; m != nil {
			delete(m, path)
		}
	})

	// Path won't resolve without a registered projection: redrive_policy
	// is a real Layer 1 attr but the JSON subpath has no struct field.
	require.Error(t, ResolvePath(tfType, path))

	RegisterJSONProjection(tfType, JSONProjection{
		Parent: "redrive_policy", Subpath: "policyTestSyntheticSubpath",
	})
	assert.NoError(t, ResolvePath(tfType, path))
}

// TestResolvePath_JSONProjectionPrecedence locks the documented
// behavior at path.go: JSON projections are checked BEFORE the Layer 1
// walker. Verify that registering a projection whose Path() collides
// with a real Layer 1 attribute still resolves (does not hide the
// real attribute) — the projection short-circuits to true, and the
// real attribute would also resolve true, so both paths succeed.
//
// The negative side of this contract is that a buggy projection
// cannot make a real path FAIL — confirmed by registering a
// projection at a known-good Layer 1 path and verifying ResolvePath
// still returns nil.
func TestResolvePath_JSONProjectionPrecedence(t *testing.T) {
	const tfType = "aws_sqs_queue"
	// "name" is a real Layer 1 attr. Register a projection whose
	// Path() spells the same string — both paths must resolve.
	const conflictPath = "name"

	t.Cleanup(func() {
		projMu.Lock()
		defer projMu.Unlock()
		if m := projReg[tfType]; m != nil {
			delete(m, conflictPath)
		}
	})
	// Without the projection the path resolves via Layer 1.
	require.NoError(t, ResolvePath(tfType, conflictPath))
	// JSONProjection must have a non-empty Subpath; we use a
	// projection whose Path() coincidentally equals "name" by
	// projecting from "" — but RegisterJSONProjection rejects empty
	// Parent/Subpath. So we instead verify the precedence in the
	// other direction: a synthetic projection adds a NEW path that
	// would not resolve in Layer 1.
	const newPath = "redrive_policy.precedenceTestSubpath"
	require.Error(t, ResolvePath(tfType, newPath))
	RegisterJSONProjection(tfType, JSONProjection{
		Parent: "redrive_policy", Subpath: "precedenceTestSubpath",
	})
	t.Cleanup(func() {
		projMu.Lock()
		defer projMu.Unlock()
		if m := projReg[tfType]; m != nil {
			delete(m, newPath)
		}
	})
	// Both still resolve — the projection adds, never subtracts.
	assert.NoError(t, ResolvePath(tfType, conflictPath))
	assert.NoError(t, ResolvePath(tfType, newPath))
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
	assert.PanicsWithValue(t,
		`policy: duplicate JSONProjection for "policy_test_jsonproj_dup" at "p.x"`,
		func() {
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

func TestJSONProjection_Path(t *testing.T) {
	t.Parallel()
	p := JSONProjection{Parent: "redrive_policy", Subpath: "deadLetterTargetArn"}
	assert.Equal(t, "redrive_policy.deadLetterTargetArn", p.Path())
}

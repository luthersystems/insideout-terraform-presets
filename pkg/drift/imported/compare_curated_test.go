package imported

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Bundle D1 (#491) curated-policy fixture tests.
//
// These tests exercise the production policy.Map entries registered for
// the five tfTypes in the bundle, end-to-end through Compare(). Each
// test asserts a *useful* drift signal: a single fixture-driven scalar
// diff under a curated path, plus the absence of a signal for
// uncurated or DriftSemanticNone fields.
//
// The fixtures live inline rather than under testdata/ — the inputs are
// small enough that inlining keeps the cause/effect of each subtest
// visible to a reviewer without a cross-file jump. If a future bundle
// grows large fixtures, promote them to testdata/<tfType>.snap.json
// alongside this file.

// fieldsByPath projects a []FieldMismatch to a map keyed by Field for
// O(1) assertion lookups in the subtests. The Compare contract already
// sorts by Field, so the projection is order-independent.
func fieldsByPath(t *testing.T, got []FieldMismatch) map[string]FieldMismatch {
	t.Helper()
	out := make(map[string]FieldMismatch, len(got))
	for _, m := range got {
		if _, dup := out[m.Field]; dup {
			t.Fatalf("Compare returned duplicate field %q — sort/dispatch bug?", m.Field)
		}
		out[m.Field] = m
	}
	return out
}

// TestCompare_Curated_AWSS3Bucket exercises the curated drift surface
// for aws_s3_bucket: a versioning flip and a server-side-encryption
// algorithm change must surface as Exact mismatches; tag drift must
// stay invisible (tagPolicy() leaves DriftSemantic=None).
func TestCompare_Curated_AWSS3Bucket(t *testing.T) {
	t.Parallel()
	snap := json.RawMessage(`{
		"arn": "arn:aws:s3:::my-bucket",
		"bucket": "my-bucket",
		"versioning": {"enabled": true, "mfa_delete": false},
		"server_side_encryption_configuration": {
			"rule": {
				"apply_server_side_encryption_by_default": {
					"sse_algorithm": "aws:kms",
					"kms_master_key_id": "arn:aws:kms:us-east-1:111:key/abc"
				},
				"bucket_key_enabled": true
			}
		},
		"tags": {"team": "infra"}
	}`)
	live := json.RawMessage(`{
		"arn": "arn:aws:s3:::my-bucket",
		"bucket": "my-bucket",
		"versioning": {"enabled": false, "mfa_delete": false},
		"server_side_encryption_configuration": {
			"rule": {
				"apply_server_side_encryption_by_default": {
					"sse_algorithm": "AES256",
					"kms_master_key_id": "arn:aws:kms:us-east-1:111:key/abc"
				},
				"bucket_key_enabled": true
			}
		},
		"tags": {"team": "platform"}
	}`)
	got := Compare("aws_s3_bucket", snap, live)
	idx := fieldsByPath(t, got)

	if _, ok := idx["versioning.enabled"]; !ok {
		t.Errorf("expected versioning.enabled mismatch; got fields: %v", keysOf(idx))
	} else {
		assert.Equal(t, true, idx["versioning.enabled"].Snapshot)
		assert.Equal(t, false, idx["versioning.enabled"].Cloud)
	}
	if _, ok := idx["server_side_encryption_configuration.rule.apply_server_side_encryption_by_default.sse_algorithm"]; !ok {
		t.Errorf("expected sse_algorithm mismatch; got fields: %v", keysOf(idx))
	}
	// tags drift must NOT appear — tagPolicy() leaves DriftSemantic=None.
	if _, ok := idx["tags"]; ok {
		t.Error("tag drift must stay invisible (tagPolicy keeps DriftSemantic=None)")
	}
}

// TestCompare_Curated_AWSDynamoDBTable exercises capacity scaling and a
// WholeList diff on a GSI's non_key_attributes.
func TestCompare_Curated_AWSDynamoDBTable(t *testing.T) {
	t.Parallel()
	snap := json.RawMessage(`{
		"arn": "arn:aws:dynamodb:us-east-1:111:table/Users",
		"name": "Users",
		"billing_mode": "PROVISIONED",
		"read_capacity": 5,
		"write_capacity": 5,
		"point_in_time_recovery": {"enabled": true},
		"global_secondary_index": {
			"name": "EmailIdx",
			"hash_key": "email",
			"projection_type": "INCLUDE",
			"non_key_attributes": ["created_at", "status"]
		}
	}`)
	live := json.RawMessage(`{
		"arn": "arn:aws:dynamodb:us-east-1:111:table/Users",
		"name": "Users",
		"billing_mode": "PROVISIONED",
		"read_capacity": 25,
		"write_capacity": 5,
		"point_in_time_recovery": {"enabled": true},
		"global_secondary_index": {
			"name": "EmailIdx",
			"hash_key": "email",
			"projection_type": "INCLUDE",
			"non_key_attributes": ["created_at", "status", "team"]
		}
	}`)
	got := Compare("aws_dynamodb_table", snap, live)
	idx := fieldsByPath(t, got)

	if m, ok := idx["read_capacity"]; !ok {
		t.Errorf("expected read_capacity mismatch; got fields: %v", keysOf(idx))
	} else {
		// JSON numbers decode to float64.
		assert.Equal(t, float64(5), m.Snapshot)
		assert.Equal(t, float64(25), m.Cloud)
	}
	gsiNKA, ok := idx["global_secondary_index.non_key_attributes"]
	require.True(t, ok, "expected non_key_attributes WholeList mismatch")
	assert.IsType(t, []any{}, gsiNKA.Snapshot,
		"WholeList output must be a []any, not a raw object")
	assert.IsType(t, []any{}, gsiNKA.Cloud)
}

// TestCompare_Curated_AWSLambdaFunction covers an Exact mismatch on
// memory_size, a WholeList mismatch on layers (ordered list of
// versioned ARNs), and confirms environment.variables stays out of the
// drift output (Sensitive must not leak).
func TestCompare_Curated_AWSLambdaFunction(t *testing.T) {
	t.Parallel()
	snap := json.RawMessage(`{
		"arn": "arn:aws:lambda:us-east-1:111:function:fn",
		"function_name": "fn",
		"runtime": "nodejs20.x",
		"memory_size": 512,
		"timeout": 30,
		"layers": ["arn:aws:lambda:us-east-1:111:layer:libA:3", "arn:aws:lambda:us-east-1:111:layer:libB:1"],
		"environment": {"variables": {"API_KEY": "super-secret"}}
	}`)
	live := json.RawMessage(`{
		"arn": "arn:aws:lambda:us-east-1:111:function:fn",
		"function_name": "fn",
		"runtime": "nodejs20.x",
		"memory_size": 1024,
		"timeout": 30,
		"layers": ["arn:aws:lambda:us-east-1:111:layer:libA:4", "arn:aws:lambda:us-east-1:111:layer:libB:1"],
		"environment": {"variables": {"API_KEY": "different-secret"}}
	}`)
	got := Compare("aws_lambda_function", snap, live)
	idx := fieldsByPath(t, got)

	require.Contains(t, idx, "memory_size")
	assert.Equal(t, float64(512), idx["memory_size"].Snapshot)
	assert.Equal(t, float64(1024), idx["memory_size"].Cloud)

	require.Contains(t, idx, "layers")
	assert.IsType(t, []any{}, idx["layers"].Snapshot)

	// environment.variables must NOT appear — Sensitive must not flow
	// through drift output.
	assert.NotContains(t, idx, "environment.variables",
		"Sensitive environment.variables must not appear in drift output")
}

// TestCompare_Curated_GoogleStorageBucket covers an Exact mismatch on
// uniform_bucket_level_access and a WholeList mismatch on cors.origin
// (whose ordering matters for the provider's diff).
func TestCompare_Curated_GoogleStorageBucket(t *testing.T) {
	t.Parallel()
	snap := json.RawMessage(`{
		"name": "my-bucket",
		"location": "US",
		"storage_class": "STANDARD",
		"uniform_bucket_level_access": true,
		"versioning": {"enabled": true},
		"cors": {
			"method": ["GET", "HEAD"],
			"origin": ["https://app.example.com"],
			"max_age_seconds": 3600
		},
		"labels": {"env": "prod", "goog-managed-by": "tf"}
	}`)
	live := json.RawMessage(`{
		"name": "my-bucket",
		"location": "US",
		"storage_class": "STANDARD",
		"uniform_bucket_level_access": false,
		"versioning": {"enabled": true},
		"cors": {
			"method": ["GET", "HEAD"],
			"origin": ["https://app.example.com", "https://admin.example.com"],
			"max_age_seconds": 3600
		},
		"labels": {"env": "prod", "goog-managed-by": "tf"}
	}`)
	got := Compare("google_storage_bucket", snap, live)
	idx := fieldsByPath(t, got)

	require.Contains(t, idx, "uniform_bucket_level_access")
	assert.Equal(t, true, idx["uniform_bucket_level_access"].Snapshot)
	assert.Equal(t, false, idx["uniform_bucket_level_access"].Cloud)

	require.Contains(t, idx, "cors.origin")
	assert.IsType(t, []any{}, idx["cors.origin"].Snapshot)

	// `labels` uses tagPolicy() → DriftSemantic=None → never emitted.
	assert.NotContains(t, idx, "labels")
}

// TestCompare_Curated_GooglePubsubTopic covers an Exact mismatch on
// message_retention_duration and a WholeList mismatch on
// message_storage_policy.allowed_persistence_regions.
func TestCompare_Curated_GooglePubsubTopic(t *testing.T) {
	t.Parallel()
	snap := json.RawMessage(`{
		"name": "projects/p/topics/orders",
		"project": "p",
		"message_retention_duration": "604800s",
		"message_storage_policy": {
			"allowed_persistence_regions": ["us-central1", "us-east1"]
		},
		"kms_key_name": "projects/p/locations/global/keyRings/r/cryptoKeys/k"
	}`)
	live := json.RawMessage(`{
		"name": "projects/p/topics/orders",
		"project": "p",
		"message_retention_duration": "86400s",
		"message_storage_policy": {
			"allowed_persistence_regions": ["us-east1"]
		},
		"kms_key_name": "projects/p/locations/global/keyRings/r/cryptoKeys/k"
	}`)
	got := Compare("google_pubsub_topic", snap, live)
	idx := fieldsByPath(t, got)

	require.Contains(t, idx, "message_retention_duration")
	assert.Equal(t, "604800s", idx["message_retention_duration"].Snapshot)
	assert.Equal(t, "86400s", idx["message_retention_duration"].Cloud)

	require.Contains(t, idx, "message_storage_policy.allowed_persistence_regions")
	assert.IsType(t, []any{}, idx["message_storage_policy.allowed_persistence_regions"].Snapshot)

	// kms_key_name is identical on both sides → not in drift output.
	assert.NotContains(t, idx, "kms_key_name")
}

// keysOf returns the sorted key set of m for diagnostic logging.
func keysOf(m map[string]FieldMismatch) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

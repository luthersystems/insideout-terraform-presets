package imported

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Bundle D1 + D2 + D3 (#491) curated-policy fixture tests.
//
// These tests exercise the production policy.Map entries registered for
// the tfTypes in each bundle, end-to-end through Compare(). Each
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
// (whose ordering matters for the provider's diff). Labels are equal
// on the user-key side (only goog-* may differ) so per-key labels
// drift stays empty.
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

	// `labels` uses gcpLabelDriftPolicy(): with identical user keys
	// (env=prod on both sides) and only goog-* matched on either
	// side, no per-key labels.* mismatch is emitted. The whole-map
	// "labels" field name is never used with LabelFilter.
	assert.NotContains(t, idx, "labels")
	assert.NotContains(t, idx, "labels.env")
	assert.NotContains(t, idx, "labels.goog-managed-by")
}

// TestCompare_Curated_GoogleStorageBucket_LifecycleWholeList covers
// the lifecycle_rule whole-list collapse: a leaf-level change (age
// 30 → 60) inside one rule of a multi-rule list emits ONE
// `lifecycle_rule` mismatch with both sides as []any, not the
// pre-#1479 fan-out into four `lifecycle_rule.action.type`,
// `lifecycle_rule.condition.age`, etc per-leaf entries.
func TestCompare_Curated_GoogleStorageBucket_LifecycleWholeList(t *testing.T) {
	t.Parallel()
	snap := json.RawMessage(`{
		"name": "my-bucket",
		"location": "US",
		"storage_class": "STANDARD",
		"lifecycle_rule": [
			{"action": {"type": "SetStorageClass", "storage_class": "NEARLINE"}, "condition": {"age": 30}},
			{"action": {"type": "Delete"}, "condition": {"age": 365}}
		]
	}`)
	live := json.RawMessage(`{
		"name": "my-bucket",
		"location": "US",
		"storage_class": "STANDARD",
		"lifecycle_rule": [
			{"action": {"type": "SetStorageClass", "storage_class": "NEARLINE"}, "condition": {"age": 60}},
			{"action": {"type": "Delete"}, "condition": {"age": 365}}
		]
	}`)
	got := Compare("google_storage_bucket", snap, live)
	idx := fieldsByPath(t, got)

	// One whole-list mismatch on the parent — no per-leaf fan-out.
	require.Contains(t, idx, "lifecycle_rule",
		"expected single lifecycle_rule WholeList mismatch")
	assert.IsType(t, []any{}, idx["lifecycle_rule"].Snapshot)
	assert.IsType(t, []any{}, idx["lifecycle_rule"].Cloud)
	assert.NotContains(t, idx, "lifecycle_rule.action.type")
	assert.NotContains(t, idx, "lifecycle_rule.condition.age")
	assert.NotContains(t, idx, "lifecycle_rule.condition.with_state")
	assert.NotContains(t, idx, "lifecycle_rule.action.storage_class")

	// Identical lifecycle on both sides → no mismatch.
	got2 := Compare("google_storage_bucket", snap, snap)
	idx2 := fieldsByPath(t, got2)
	assert.NotContains(t, idx2, "lifecycle_rule")
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

// --- Bundle D2 (#491) ------------------------------------------------

// TestCompare_Curated_AWSCloudwatchLogGroup_Exact exercises an Exact
// retention drift on aws_cloudwatch_log_group. Tag drift must stay
// invisible (tagPolicy() leaves DriftSemantic=None).
func TestCompare_Curated_AWSCloudwatchLogGroup_Exact(t *testing.T) {
	t.Parallel()
	snap := json.RawMessage(`{
		"arn": "arn:aws:logs:us-east-1:111:log-group:/aws/lambda/fn:*",
		"name": "/aws/lambda/fn",
		"kms_key_id": "arn:aws:kms:us-east-1:111:key/abc",
		"retention_in_days": 14,
		"log_group_class": "STANDARD",
		"skip_destroy": false,
		"tags": {"team": "infra"}
	}`)
	live := json.RawMessage(`{
		"arn": "arn:aws:logs:us-east-1:111:log-group:/aws/lambda/fn:*",
		"name": "/aws/lambda/fn",
		"kms_key_id": "arn:aws:kms:us-east-1:111:key/abc",
		"retention_in_days": 90,
		"log_group_class": "STANDARD",
		"skip_destroy": false,
		"tags": {"team": "platform"}
	}`)
	got := Compare("aws_cloudwatch_log_group", snap, live)
	idx := fieldsByPath(t, got)

	require.Contains(t, idx, "retention_in_days",
		"expected retention_in_days mismatch; got fields: %v", keysOf(idx))
	assert.Equal(t, float64(14), idx["retention_in_days"].Snapshot)
	assert.Equal(t, float64(90), idx["retention_in_days"].Cloud)

	// kms_key_id is identical → no mismatch.
	assert.NotContains(t, idx, "kms_key_id")
	// tags use tagPolicy() (DriftSemantic=None) → never emitted.
	assert.NotContains(t, idx, "tags")
}

// TestCompare_Curated_AWSSecretsmanagerSecret_Exact exercises an Exact
// drift on the rotation-window knob and confirms the resource policy
// JSON document diff also surfaces. The secret payload lives on a
// separate resource (aws_secretsmanager_secret_version) and is not
// curated on this map, so no Sensitive-leak path applies here.
func TestCompare_Curated_AWSSecretsmanagerSecret_Exact(t *testing.T) {
	t.Parallel()
	snap := json.RawMessage(`{
		"arn": "arn:aws:secretsmanager:us-east-1:111:secret:db-creds-AbCdEf",
		"name": "db-creds",
		"kms_key_id": "arn:aws:kms:us-east-1:111:key/abc",
		"description": "primary db credentials",
		"recovery_window_in_days": 30,
		"policy": "{\"Version\":\"2012-10-17\",\"Statement\":[]}",
		"tags": {"team": "infra"}
	}`)
	live := json.RawMessage(`{
		"arn": "arn:aws:secretsmanager:us-east-1:111:secret:db-creds-AbCdEf",
		"name": "db-creds",
		"kms_key_id": "arn:aws:kms:us-east-1:111:key/abc",
		"description": "primary db credentials",
		"recovery_window_in_days": 7,
		"policy": "{\"Version\":\"2012-10-17\",\"Statement\":[{\"Effect\":\"Deny\",\"Principal\":\"*\",\"Action\":\"*\"}]}",
		"tags": {"team": "infra"}
	}`)
	got := Compare("aws_secretsmanager_secret", snap, live)
	idx := fieldsByPath(t, got)

	require.Contains(t, idx, "recovery_window_in_days")
	assert.Equal(t, float64(30), idx["recovery_window_in_days"].Snapshot)
	assert.Equal(t, float64(7), idx["recovery_window_in_days"].Cloud)

	require.Contains(t, idx, "policy",
		"expected resource-policy JSON diff to surface as an Exact mismatch")

	// Identity / wiring values are equal → no mismatch.
	assert.NotContains(t, idx, "kms_key_id")
	assert.NotContains(t, idx, "arn")
}

// TestCompare_Curated_AWSSQSQueue_Exact exercises an Exact drift on a
// flat scalar (visibility_timeout_seconds) and confirms the equal
// kms_master_key_id wiring leaf does not surface. The redrive_policy.*
// JSON-projection paths intentionally do not produce a signal through
// the current comparator (see policy file comment) — this test pins
// that observable behavior so a future projection-aware comparator
// refactor surfaces here as an intentional diff.
func TestCompare_Curated_AWSSQSQueue_Exact(t *testing.T) {
	t.Parallel()
	snap := json.RawMessage(`{
		"arn": "arn:aws:sqs:us-east-1:111:work-queue",
		"name": "work-queue",
		"kms_master_key_id": "alias/aws/sqs",
		"visibility_timeout_seconds": 30,
		"delay_seconds": 0,
		"message_retention_seconds": 345600,
		"max_message_size": 262144,
		"fifo_queue": false,
		"content_based_deduplication": false,
		"redrive_policy": "{\"deadLetterTargetArn\":\"arn:aws:sqs:us-east-1:111:dlq\",\"maxReceiveCount\":5}",
		"tags": {"team": "infra"}
	}`)
	live := json.RawMessage(`{
		"arn": "arn:aws:sqs:us-east-1:111:work-queue",
		"name": "work-queue",
		"kms_master_key_id": "alias/aws/sqs",
		"visibility_timeout_seconds": 120,
		"delay_seconds": 0,
		"message_retention_seconds": 345600,
		"max_message_size": 262144,
		"fifo_queue": false,
		"content_based_deduplication": false,
		"redrive_policy": "{\"deadLetterTargetArn\":\"arn:aws:sqs:us-east-1:111:dlq\",\"maxReceiveCount\":5}",
		"tags": {"team": "infra"}
	}`)
	got := Compare("aws_sqs_queue", snap, live)
	idx := fieldsByPath(t, got)

	require.Contains(t, idx, "visibility_timeout_seconds")
	assert.Equal(t, float64(30), idx["visibility_timeout_seconds"].Snapshot)
	assert.Equal(t, float64(120), idx["visibility_timeout_seconds"].Cloud)

	// Wiring / identity equal → no mismatch.
	assert.NotContains(t, idx, "kms_master_key_id")
	assert.NotContains(t, idx, "arn")

	// JSON-projection paths: the parent is a JSON-encoded string, not a
	// nested map. Pin current behavior — these do not surface today.
	assert.NotContains(t, idx, "redrive_policy.deadLetterTargetArn",
		"redrive_policy JSON projection should not surface through the "+
			"current comparator; if this fails, projection-aware traversal "+
			"has landed and the policy file comment needs updating")
	assert.NotContains(t, idx, "redrive_policy.maxReceiveCount")
}

// TestCompare_Curated_GoogleSecretManagerSecret_Exact exercises an
// Exact drift on the rotation-period knob plus a per-key labels drift
// (env=prod → env=staging) via the gcpLabelDriftPolicy() adoption.
// goog-* control-plane labels stay filtered. As with the AWS
// counterpart, the secret payload lives on a separate resource
// (google_secret_manager_secret_version) — no Sensitive-leak path
// applies here.
func TestCompare_Curated_GoogleSecretManagerSecret_Exact(t *testing.T) {
	t.Parallel()
	snap := json.RawMessage(`{
		"name": "projects/p/secrets/api-key",
		"secret_id": "api-key",
		"project": "p",
		"rotation": {
			"rotation_period": "2592000s",
			"next_rotation_time": "2026-06-01T00:00:00Z"
		},
		"ttl": "7776000s",
		"labels": {"env": "prod", "goog-managed-by": "tf"}
	}`)
	live := json.RawMessage(`{
		"name": "projects/p/secrets/api-key",
		"secret_id": "api-key",
		"project": "p",
		"rotation": {
			"rotation_period": "604800s",
			"next_rotation_time": "2026-06-01T00:00:00Z"
		},
		"ttl": "7776000s",
		"labels": {"env": "staging", "goog-managed-by": "tf"}
	}`)
	got := Compare("google_secret_manager_secret", snap, live)
	idx := fieldsByPath(t, got)

	require.Contains(t, idx, "rotation.rotation_period")
	assert.Equal(t, "2592000s", idx["rotation.rotation_period"].Snapshot)
	assert.Equal(t, "604800s", idx["rotation.rotation_period"].Cloud)

	// Identity / TTL identical → no mismatch.
	assert.NotContains(t, idx, "project")
	assert.NotContains(t, idx, "ttl")
	// Per-key labels drift surfaces; goog-* prefix is filtered.
	require.Contains(t, idx, "labels.env",
		"expected per-key labels.env mismatch via gcpLabelDriftPolicy()")
	assert.Equal(t, "prod", idx["labels.env"].Snapshot)
	assert.Equal(t, "staging", idx["labels.env"].Cloud)
	// Whole-map "labels" field never emitted with LabelFilter — only per-key.
	assert.NotContains(t, idx, "labels")
	// goog-managed-by is identical AND filtered → never emitted either way.
	assert.NotContains(t, idx, "labels.goog-managed-by")
}

// TestCompare_Curated_GoogleComputeNetwork_Exact exercises an Exact
// drift on routing_mode and confirms identical identity fields don't
// surface. google_compute_network has no labels and no list-valued
// curated fields, so neither LabelFilter nor WholeList comes into play.
func TestCompare_Curated_GoogleComputeNetwork_Exact(t *testing.T) {
	t.Parallel()
	snap := json.RawMessage(`{
		"name": "vpc-prod",
		"self_link": "https://www.googleapis.com/compute/v1/projects/p/global/networks/vpc-prod",
		"project": "p",
		"auto_create_subnetworks": false,
		"routing_mode": "REGIONAL",
		"mtu": 1460,
		"description": "production VPC"
	}`)
	live := json.RawMessage(`{
		"name": "vpc-prod",
		"self_link": "https://www.googleapis.com/compute/v1/projects/p/global/networks/vpc-prod",
		"project": "p",
		"auto_create_subnetworks": false,
		"routing_mode": "GLOBAL",
		"mtu": 1460,
		"description": "production VPC"
	}`)
	got := Compare("google_compute_network", snap, live)
	idx := fieldsByPath(t, got)

	require.Contains(t, idx, "routing_mode")
	assert.Equal(t, "REGIONAL", idx["routing_mode"].Snapshot)
	assert.Equal(t, "GLOBAL", idx["routing_mode"].Cloud)

	// Identity / unchanged knobs → no mismatch.
	assert.NotContains(t, idx, "project")
	assert.NotContains(t, idx, "mtu")
	assert.NotContains(t, idx, "description")
}

// --- Bundle D3 (#491) ------------------------------------------------

// TestCompare_Curated_GoogleComputeAddress_Exact exercises an Exact
// drift on the network_tier knob and confirms identical identity /
// wiring fields don't surface. google_compute_address has no
// list-valued curated fields, so WholeList does not come into play;
// labels stay at tagPolicy() (DriftSemantic=None).
func TestCompare_Curated_GoogleComputeAddress_Exact(t *testing.T) {
	t.Parallel()
	snap := json.RawMessage(`{
		"name": "ingress-ip",
		"self_link": "https://www.googleapis.com/compute/v1/projects/p/regions/us-east1/addresses/ingress-ip",
		"project": "p",
		"region": "us-east1",
		"address": "10.0.0.5",
		"address_type": "INTERNAL",
		"network_tier": "PREMIUM",
		"subnetwork": "projects/p/regions/us-east1/subnetworks/private",
		"labels": {"env": "prod"}
	}`)
	live := json.RawMessage(`{
		"name": "ingress-ip",
		"self_link": "https://www.googleapis.com/compute/v1/projects/p/regions/us-east1/addresses/ingress-ip",
		"project": "p",
		"region": "us-east1",
		"address": "10.0.0.5",
		"address_type": "INTERNAL",
		"network_tier": "STANDARD",
		"subnetwork": "projects/p/regions/us-east1/subnetworks/private",
		"labels": {"env": "staging"}
	}`)
	got := Compare("google_compute_address", snap, live)
	idx := fieldsByPath(t, got)

	require.Contains(t, idx, "network_tier")
	assert.Equal(t, "PREMIUM", idx["network_tier"].Snapshot)
	assert.Equal(t, "STANDARD", idx["network_tier"].Cloud)

	// Identity / wiring / address knobs are identical → no mismatch.
	assert.NotContains(t, idx, "project")
	assert.NotContains(t, idx, "region")
	assert.NotContains(t, idx, "address")
	assert.NotContains(t, idx, "subnetwork")
	// Labels use tagPolicy() → DriftSemantic=None → never emitted.
	assert.NotContains(t, idx, "labels")
}

// TestCompare_Curated_GooglePubsubSubscription_Exact exercises an
// Exact drift on ack_deadline_seconds and confirms that push_config
// Sensitive fields (push_endpoint, attributes, oidc_token.audience)
// do not surface — they stay DriftSemantic=None to avoid echoing
// bearer tokens through drift output. Mirrors the
// aws_lambda_function.environment.variables guarantee from D1.
func TestCompare_Curated_GooglePubsubSubscription_Exact(t *testing.T) {
	t.Parallel()
	snap := json.RawMessage(`{
		"name": "projects/p/subscriptions/orders-sub",
		"project": "p",
		"topic": "projects/p/topics/orders",
		"ack_deadline_seconds": 10,
		"message_retention_duration": "604800s",
		"enable_exactly_once_delivery": false,
		"push_config": {
			"push_endpoint": "https://app.example.com/push?token=secret-A",
			"attributes": {"x-goog-version": "v1", "x-tenant-token": "tenant-A"},
			"oidc_token": {"audience": "tenant-A-aud", "service_account_email": "pusher@p.iam.gserviceaccount.com"}
		},
		"labels": {"env": "prod"}
	}`)
	live := json.RawMessage(`{
		"name": "projects/p/subscriptions/orders-sub",
		"project": "p",
		"topic": "projects/p/topics/orders",
		"ack_deadline_seconds": 60,
		"message_retention_duration": "604800s",
		"enable_exactly_once_delivery": false,
		"push_config": {
			"push_endpoint": "https://app.example.com/push?token=secret-B",
			"attributes": {"x-goog-version": "v1", "x-tenant-token": "tenant-B"},
			"oidc_token": {"audience": "tenant-B-aud", "service_account_email": "pusher@p.iam.gserviceaccount.com"}
		},
		"labels": {"env": "prod"}
	}`)
	got := Compare("google_pubsub_subscription", snap, live)
	idx := fieldsByPath(t, got)

	require.Contains(t, idx, "ack_deadline_seconds")
	assert.Equal(t, float64(10), idx["ack_deadline_seconds"].Snapshot)
	assert.Equal(t, float64(60), idx["ack_deadline_seconds"].Cloud)

	// Identity / topic wiring / retention are identical → no mismatch.
	assert.NotContains(t, idx, "project")
	assert.NotContains(t, idx, "topic")
	assert.NotContains(t, idx, "message_retention_duration")

	// Sensitive push_config fields must NOT surface — bearer tokens
	// and per-tenant identifiers must not leak through drift output.
	assert.NotContains(t, idx, "push_config.push_endpoint",
		"Sensitive push_endpoint must not appear in drift output")
	assert.NotContains(t, idx, "push_config.attributes",
		"Sensitive push_config.attributes must not appear in drift output")
	assert.NotContains(t, idx, "push_config.oidc_token.audience",
		"Sensitive oidc_token.audience must not appear in drift output")

	// Labels use tagPolicy() → DriftSemantic=None → never emitted.
	assert.NotContains(t, idx, "labels")
}

// TestCompare_Curated_GoogleComputeFirewall_WholeList exercises a
// WholeList mismatch on source_ranges (CIDR set) and an Exact
// mismatch on the disabled scalar, and confirms equal selectors don't
// surface.
func TestCompare_Curated_GoogleComputeFirewall_WholeList(t *testing.T) {
	t.Parallel()
	snap := json.RawMessage(`{
		"name": "fw-allow-ssh",
		"self_link": "https://www.googleapis.com/compute/v1/projects/p/global/firewalls/fw-allow-ssh",
		"project": "p",
		"network": "projects/p/global/networks/vpc-prod",
		"direction": "INGRESS",
		"priority": 1000,
		"disabled": false,
		"source_ranges": ["10.0.0.0/8", "172.16.0.0/12"],
		"target_tags": ["ssh-allowed"]
	}`)
	live := json.RawMessage(`{
		"name": "fw-allow-ssh",
		"self_link": "https://www.googleapis.com/compute/v1/projects/p/global/firewalls/fw-allow-ssh",
		"project": "p",
		"network": "projects/p/global/networks/vpc-prod",
		"direction": "INGRESS",
		"priority": 1000,
		"disabled": true,
		"source_ranges": ["10.0.0.0/8"],
		"target_tags": ["ssh-allowed"]
	}`)
	got := Compare("google_compute_firewall", snap, live)
	idx := fieldsByPath(t, got)

	require.Contains(t, idx, "disabled")
	assert.Equal(t, false, idx["disabled"].Snapshot)
	assert.Equal(t, true, idx["disabled"].Cloud)

	require.Contains(t, idx, "source_ranges")
	assert.IsType(t, []any{}, idx["source_ranges"].Snapshot,
		"WholeList output must be a []any, not a raw object")
	assert.IsType(t, []any{}, idx["source_ranges"].Cloud)

	// Equal selectors → no mismatch.
	assert.NotContains(t, idx, "target_tags")
	// Identity / wiring identical → no mismatch.
	assert.NotContains(t, idx, "project")
	assert.NotContains(t, idx, "network")
	assert.NotContains(t, idx, "direction")
}

// TestCompare_Curated_GoogleComputeForwardingRule_WholeList covers
// a WholeList mismatch on the ports list and an Exact mismatch on
// network_tier, and confirms identical identity / wiring don't
// surface.
func TestCompare_Curated_GoogleComputeForwardingRule_WholeList(t *testing.T) {
	t.Parallel()
	snap := json.RawMessage(`{
		"name": "ingress-fr",
		"self_link": "https://www.googleapis.com/compute/v1/projects/p/regions/us-east1/forwardingRules/ingress-fr",
		"project": "p",
		"region": "us-east1",
		"target": "projects/p/regions/us-east1/targetPools/web",
		"ip_address": "203.0.113.10",
		"ip_protocol": "TCP",
		"load_balancing_scheme": "EXTERNAL",
		"network_tier": "PREMIUM",
		"ports": ["80", "443"],
		"labels": {"env": "prod"}
	}`)
	live := json.RawMessage(`{
		"name": "ingress-fr",
		"self_link": "https://www.googleapis.com/compute/v1/projects/p/regions/us-east1/forwardingRules/ingress-fr",
		"project": "p",
		"region": "us-east1",
		"target": "projects/p/regions/us-east1/targetPools/web",
		"ip_address": "203.0.113.10",
		"ip_protocol": "TCP",
		"load_balancing_scheme": "EXTERNAL",
		"network_tier": "STANDARD",
		"ports": ["80", "443", "8080"],
		"labels": {"env": "prod"}
	}`)
	got := Compare("google_compute_forwarding_rule", snap, live)
	idx := fieldsByPath(t, got)

	require.Contains(t, idx, "network_tier")
	assert.Equal(t, "PREMIUM", idx["network_tier"].Snapshot)
	assert.Equal(t, "STANDARD", idx["network_tier"].Cloud)

	require.Contains(t, idx, "ports")
	assert.IsType(t, []any{}, idx["ports"].Snapshot)
	assert.IsType(t, []any{}, idx["ports"].Cloud)

	// Identity / IP wiring / scheme are identical → no mismatch.
	assert.NotContains(t, idx, "project")
	assert.NotContains(t, idx, "region")
	assert.NotContains(t, idx, "target")
	assert.NotContains(t, idx, "ip_address")
	assert.NotContains(t, idx, "load_balancing_scheme")
	// Labels use tagPolicy() → DriftSemantic=None → never emitted.
	assert.NotContains(t, idx, "labels")
}

// TestCompare_Curated_GoogleComputeHealthCheck_Exact exercises an
// Exact drift on healthy_threshold and a WholeList drift on
// source_regions, and confirms identical probe-interval scalars
// don't surface.
func TestCompare_Curated_GoogleComputeHealthCheck_Exact(t *testing.T) {
	t.Parallel()
	snap := json.RawMessage(`{
		"name": "tcp-hc",
		"self_link": "https://www.googleapis.com/compute/v1/projects/p/global/healthChecks/tcp-hc",
		"project": "p",
		"type": "TCP",
		"check_interval_sec": 10,
		"timeout_sec": 5,
		"healthy_threshold": 2,
		"unhealthy_threshold": 3,
		"source_regions": ["us-east1", "us-central1"]
	}`)
	live := json.RawMessage(`{
		"name": "tcp-hc",
		"self_link": "https://www.googleapis.com/compute/v1/projects/p/global/healthChecks/tcp-hc",
		"project": "p",
		"type": "TCP",
		"check_interval_sec": 10,
		"timeout_sec": 5,
		"healthy_threshold": 5,
		"unhealthy_threshold": 3,
		"source_regions": ["us-east1"]
	}`)
	got := Compare("google_compute_health_check", snap, live)
	idx := fieldsByPath(t, got)

	require.Contains(t, idx, "healthy_threshold")
	assert.Equal(t, float64(2), idx["healthy_threshold"].Snapshot)
	assert.Equal(t, float64(5), idx["healthy_threshold"].Cloud)

	require.Contains(t, idx, "source_regions")
	assert.IsType(t, []any{}, idx["source_regions"].Snapshot)
	assert.IsType(t, []any{}, idx["source_regions"].Cloud)

	// Identity / unchanged probe-interval scalars → no mismatch.
	assert.NotContains(t, idx, "project")
	assert.NotContains(t, idx, "type")
	assert.NotContains(t, idx, "check_interval_sec")
	assert.NotContains(t, idx, "timeout_sec")
	assert.NotContains(t, idx, "unhealthy_threshold")
}

// --- Bundle D4 / IAM-binding curation (#491) -----------------------------
//
// Each IAM binding/member type is the (parent × role × member) tuple plus
// a `members` list (binding-flavoured) or a singleton `member` scalar
// (member-flavoured). Pre-#491 every field carried DriftSemantic=None,
// so an out-of-band IAM edit slipped past the curated comparator
// entirely. Bundle D4 flips the role / member / members fields onto
// the comparator so a flipped role binding or a deleted/added principal
// surfaces as a security-pillar drift signal.
//
// The subtests below pin: (1) role / member flips on each *_iam_member
// type emit one Exact mismatch each, (2) the WholeList shape on
// *_iam_binding `members` collapses a per-element change into a single
// list mismatch (no per-element fan-out), and (3) identical tuples
// emit zero mismatches so unchanged bindings stay quiet.

// TestCompare_Curated_GoogleProjectIAMMember_Exact exercises the
// canonical project-scope IAM member curation: a role flip emits one
// Exact mismatch on `role`, and an identical binding emits nothing.
func TestCompare_Curated_GoogleProjectIAMMember_Exact(t *testing.T) {
	t.Parallel()
	snap := json.RawMessage(`{
		"id": "p/roles/storage.admin/user:dev@example.com",
		"etag": "BwAbCdEf=",
		"project": "p",
		"role": "roles/storage.admin",
		"member": "user:dev@example.com"
	}`)
	live := json.RawMessage(`{
		"id": "p/roles/storage.objectViewer/user:dev@example.com",
		"etag": "BwAbCdEf=",
		"project": "p",
		"role": "roles/storage.objectViewer",
		"member": "user:dev@example.com"
	}`)
	got := Compare("google_project_iam_member", snap, live)
	idx := fieldsByPath(t, got)

	require.Contains(t, idx, "role",
		"expected role mismatch; got fields: %v", keysOf(idx))
	assert.Equal(t, "roles/storage.admin", idx["role"].Snapshot)
	assert.Equal(t, "roles/storage.objectViewer", idx["role"].Cloud)

	// Identity / member identical → no mismatch.
	assert.NotContains(t, idx, "project")
	assert.NotContains(t, idx, "member")
	// id/etag are DriftSemantic=None → never emitted.
	assert.NotContains(t, idx, "id")
	assert.NotContains(t, idx, "etag")

	// Identical binding on both sides → no mismatch at all.
	got2 := Compare("google_project_iam_member", snap, snap)
	assert.Empty(t, got2, "identical IAM binding must emit no drift")
}

// TestCompare_Curated_GoogleStorageBucketIAMMember_Exact exercises a
// member flip (e.g. an out-of-band edit pointing the binding at a
// different principal) — Exact mismatch on `member`, identity unchanged.
func TestCompare_Curated_GoogleStorageBucketIAMMember_Exact(t *testing.T) {
	t.Parallel()
	snap := json.RawMessage(`{
		"id": "b/my-bucket/roles/storage.objectViewer/user:reader-A@example.com",
		"etag": "BwAbCdEf=",
		"bucket": "my-bucket",
		"role": "roles/storage.objectViewer",
		"member": "user:reader-A@example.com"
	}`)
	live := json.RawMessage(`{
		"id": "b/my-bucket/roles/storage.objectViewer/user:reader-B@example.com",
		"etag": "BwAbCdEf=",
		"bucket": "my-bucket",
		"role": "roles/storage.objectViewer",
		"member": "user:reader-B@example.com"
	}`)
	got := Compare("google_storage_bucket_iam_member", snap, live)
	idx := fieldsByPath(t, got)

	require.Contains(t, idx, "member",
		"expected member mismatch; got fields: %v", keysOf(idx))
	assert.Equal(t, "user:reader-A@example.com", idx["member"].Snapshot)
	assert.Equal(t, "user:reader-B@example.com", idx["member"].Cloud)

	// Identity / role unchanged → no mismatch.
	assert.NotContains(t, idx, "bucket")
	assert.NotContains(t, idx, "role")
	assert.NotContains(t, idx, "id")
	assert.NotContains(t, idx, "etag")
}

// TestCompare_Curated_GoogleCloudRunV2ServiceIAMMember_Exact exercises
// a role flip on the Cloud Run v2 IAM-member type.
func TestCompare_Curated_GoogleCloudRunV2ServiceIAMMember_Exact(t *testing.T) {
	t.Parallel()
	snap := json.RawMessage(`{
		"id": "projects/p/locations/us-east1/services/api/roles/run.invoker/user:caller@example.com",
		"etag": "BwAbCdEf=",
		"name": "api",
		"location": "us-east1",
		"project": "p",
		"role": "roles/run.invoker",
		"member": "user:caller@example.com"
	}`)
	live := json.RawMessage(`{
		"id": "projects/p/locations/us-east1/services/api/roles/run.developer/user:caller@example.com",
		"etag": "BwAbCdEf=",
		"name": "api",
		"location": "us-east1",
		"project": "p",
		"role": "roles/run.developer",
		"member": "user:caller@example.com"
	}`)
	got := Compare("google_cloud_run_v2_service_iam_member", snap, live)
	idx := fieldsByPath(t, got)

	require.Contains(t, idx, "role")
	assert.Equal(t, "roles/run.invoker", idx["role"].Snapshot)
	assert.Equal(t, "roles/run.developer", idx["role"].Cloud)

	// Identity tuple unchanged → no mismatch.
	assert.NotContains(t, idx, "name")
	assert.NotContains(t, idx, "location")
	assert.NotContains(t, idx, "project")
	assert.NotContains(t, idx, "member")
}

// TestCompare_Curated_GoogleCloudfunctions2FunctionIAMMember_Exact
// exercises a member flip on the Cloud Functions Gen-2 IAM-member type.
func TestCompare_Curated_GoogleCloudfunctions2FunctionIAMMember_Exact(t *testing.T) {
	t.Parallel()
	snap := json.RawMessage(`{
		"id": "projects/p/locations/us-east1/functions/fn/roles/cloudfunctions.invoker/serviceAccount:caller-A@p.iam.gserviceaccount.com",
		"etag": "BwAbCdEf=",
		"cloud_function": "projects/p/locations/us-east1/functions/fn",
		"location": "us-east1",
		"project": "p",
		"role": "roles/cloudfunctions.invoker",
		"member": "serviceAccount:caller-A@p.iam.gserviceaccount.com"
	}`)
	live := json.RawMessage(`{
		"id": "projects/p/locations/us-east1/functions/fn/roles/cloudfunctions.invoker/serviceAccount:caller-B@p.iam.gserviceaccount.com",
		"etag": "BwAbCdEf=",
		"cloud_function": "projects/p/locations/us-east1/functions/fn",
		"location": "us-east1",
		"project": "p",
		"role": "roles/cloudfunctions.invoker",
		"member": "serviceAccount:caller-B@p.iam.gserviceaccount.com"
	}`)
	got := Compare("google_cloudfunctions2_function_iam_member", snap, live)
	idx := fieldsByPath(t, got)

	require.Contains(t, idx, "member")
	assert.Equal(t, "serviceAccount:caller-A@p.iam.gserviceaccount.com", idx["member"].Snapshot)
	assert.Equal(t, "serviceAccount:caller-B@p.iam.gserviceaccount.com", idx["member"].Cloud)

	// Identity / role unchanged → no mismatch.
	assert.NotContains(t, idx, "cloud_function")
	assert.NotContains(t, idx, "location")
	assert.NotContains(t, idx, "project")
	assert.NotContains(t, idx, "role")
}

// TestCompare_Curated_GoogleSecretManagerSecretIAMMember_Exact
// exercises a role flip on the Secret Manager IAM-member type.
func TestCompare_Curated_GoogleSecretManagerSecretIAMMember_Exact(t *testing.T) {
	t.Parallel()
	snap := json.RawMessage(`{
		"id": "projects/p/secrets/api-key/roles/secretmanager.secretAccessor/serviceAccount:reader@p.iam.gserviceaccount.com",
		"etag": "BwAbCdEf=",
		"project": "p",
		"secret_id": "api-key",
		"role": "roles/secretmanager.secretAccessor",
		"member": "serviceAccount:reader@p.iam.gserviceaccount.com"
	}`)
	live := json.RawMessage(`{
		"id": "projects/p/secrets/api-key/roles/secretmanager.admin/serviceAccount:reader@p.iam.gserviceaccount.com",
		"etag": "BwAbCdEf=",
		"project": "p",
		"secret_id": "api-key",
		"role": "roles/secretmanager.admin",
		"member": "serviceAccount:reader@p.iam.gserviceaccount.com"
	}`)
	got := Compare("google_secret_manager_secret_iam_member", snap, live)
	idx := fieldsByPath(t, got)

	require.Contains(t, idx, "role")
	assert.Equal(t, "roles/secretmanager.secretAccessor", idx["role"].Snapshot)
	assert.Equal(t, "roles/secretmanager.admin", idx["role"].Cloud)

	// Identity / member unchanged → no mismatch.
	assert.NotContains(t, idx, "project")
	assert.NotContains(t, idx, "secret_id")
	assert.NotContains(t, idx, "member")
}

// TestCompare_Curated_GoogleKMSCryptoKeyIAMBinding_WholeList exercises
// the `members` WholeList collapse on the KMS-key IAM-binding type: a
// single added principal emits ONE `members` mismatch with both sides
// as []any, not a per-element fan-out. Also confirms a role flip emits
// an Exact mismatch alongside.
func TestCompare_Curated_GoogleKMSCryptoKeyIAMBinding_WholeList(t *testing.T) {
	t.Parallel()
	snap := json.RawMessage(`{
		"id": "projects/p/locations/global/keyRings/kr/cryptoKeys/k/roles/cloudkms.cryptoKeyEncrypterDecrypter",
		"etag": "BwAbCdEf=",
		"crypto_key_id": "projects/p/locations/global/keyRings/kr/cryptoKeys/k",
		"role": "roles/cloudkms.cryptoKeyEncrypterDecrypter",
		"members": [
			"serviceAccount:writer@p.iam.gserviceaccount.com",
			"serviceAccount:reader@p.iam.gserviceaccount.com"
		]
	}`)
	live := json.RawMessage(`{
		"id": "projects/p/locations/global/keyRings/kr/cryptoKeys/k/roles/cloudkms.cryptoKeyEncrypterDecrypter",
		"etag": "BwAbCdEf=",
		"crypto_key_id": "projects/p/locations/global/keyRings/kr/cryptoKeys/k",
		"role": "roles/cloudkms.cryptoKeyEncrypterDecrypter",
		"members": [
			"serviceAccount:writer@p.iam.gserviceaccount.com",
			"serviceAccount:reader@p.iam.gserviceaccount.com",
			"serviceAccount:unauthorized@p.iam.gserviceaccount.com"
		]
	}`)
	got := Compare("google_kms_crypto_key_iam_binding", snap, live)
	idx := fieldsByPath(t, got)

	require.Contains(t, idx, "members",
		"expected single members WholeList mismatch; got: %v", keysOf(idx))
	assert.IsType(t, []any{}, idx["members"].Snapshot,
		"WholeList output must be a []any, not a raw scalar")
	assert.IsType(t, []any{}, idx["members"].Cloud)

	// No per-element fan-out under the members path.
	assert.NotContains(t, idx, "members.0")
	assert.NotContains(t, idx, "members.1")
	assert.NotContains(t, idx, "members.2")

	// Identity / role unchanged → no mismatch.
	assert.NotContains(t, idx, "crypto_key_id")
	assert.NotContains(t, idx, "role")

	// Identical binding on both sides → no mismatch at all.
	got2 := Compare("google_kms_crypto_key_iam_binding", snap, snap)
	assert.Empty(t, got2, "identical IAM binding must emit no drift")
}

// TestCompare_Curated_GoogleSecretManagerSecretIAMBinding_WholeList
// exercises the `members` WholeList collapse alongside a role flip on
// the Secret Manager IAM-binding type.
func TestCompare_Curated_GoogleSecretManagerSecretIAMBinding_WholeList(t *testing.T) {
	t.Parallel()
	snap := json.RawMessage(`{
		"id": "projects/p/secrets/api-key/roles/secretmanager.secretAccessor",
		"etag": "BwAbCdEf=",
		"project": "p",
		"secret_id": "api-key",
		"role": "roles/secretmanager.secretAccessor",
		"members": [
			"serviceAccount:reader-A@p.iam.gserviceaccount.com",
			"serviceAccount:reader-B@p.iam.gserviceaccount.com"
		]
	}`)
	live := json.RawMessage(`{
		"id": "projects/p/secrets/api-key/roles/secretmanager.admin",
		"etag": "BwAbCdEf=",
		"project": "p",
		"secret_id": "api-key",
		"role": "roles/secretmanager.admin",
		"members": [
			"serviceAccount:reader-A@p.iam.gserviceaccount.com"
		]
	}`)
	got := Compare("google_secret_manager_secret_iam_binding", snap, live)
	idx := fieldsByPath(t, got)

	require.Contains(t, idx, "role",
		"expected role Exact mismatch; got: %v", keysOf(idx))
	assert.Equal(t, "roles/secretmanager.secretAccessor", idx["role"].Snapshot)
	assert.Equal(t, "roles/secretmanager.admin", idx["role"].Cloud)

	require.Contains(t, idx, "members",
		"expected members WholeList mismatch; got: %v", keysOf(idx))
	assert.IsType(t, []any{}, idx["members"].Snapshot)
	assert.IsType(t, []any{}, idx["members"].Cloud)

	// No per-element fan-out under members.
	assert.NotContains(t, idx, "members.0")
	assert.NotContains(t, idx, "members.1")

	// Identity unchanged → no mismatch.
	assert.NotContains(t, idx, "project")
	assert.NotContains(t, idx, "secret_id")
}

// keysOf returns the sorted key set of m for diagnostic logging.
func keysOf(m map[string]FieldMismatch) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

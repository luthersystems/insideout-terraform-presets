package composer

// Tests for the IR → terraform translation bugs catalogued in
// luthersystems/insideout-terraform-presets#131 (the upstream audit ticket
// spawned by reliable#1149).
//
// Each Test* function below corresponds to one bug in that audit:
//   1 — DynamoDB billing_mode lower-case → uppercase validation reject
//   2 — OpenSearch storage_size "1TB" → tonumber() reject
//   3 — GCP CloudCDN default_ttl string → number reject
//   4 — ElastiCache replicas string → number reject
//   5 — RDS key names (node_cpu_size etc.) didn't match module variables
//   6 — ElastiCache key names (node_size, orphan storage_size)
//   7 — CloudFront default_ttl key name + value type
//   8 — MSK retention_period emitted under non-existent variable
//   9 — Lambda memory_size / timeout silently dropped on bad input
//
// Tests assert on the mapper's *intended output* — i.e., the keys it writes
// to the returned map. The companion cross-module test in compose_stack_test.go
// (TestMapperKeysSubsetOfModuleVariables) walks the embedded preset bundle
// and verifies every key the mapper emits is a declared module variable, so
// renaming a variable upstream without updating the mapper here fails CI.

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Inline-struct builders. Config's sub-configs are anonymous structs, so
// every test that touches them has to redeclare the full type. These helpers
// absorb the boilerplate.
// ---------------------------------------------------------------------------

func ddbCfg(typ string) *Config {
	return &Config{
		AWSDynamoDB: &struct {
			Type string `json:"type,omitempty"`
		}{Type: typ},
	}
}

func openSearchCfg(storage string) *Config {
	return &Config{
		AWSOpenSearch: &struct {
			DeploymentType string `json:"deploymentType,omitempty"`
			InstanceType   string `json:"instanceType,omitempty"`
			StorageSize    string `json:"storageSize,omitempty"`
			MultiAZ        *bool  `json:"multiAz,omitempty"`
		}{StorageSize: storage},
	}
}

func gcpCdnCfg(ttl string) *Config {
	return &Config{
		GCPCloudCDN: &struct {
			DefaultTtl string `json:"defaultTtl,omitempty"`
			OriginPath string `json:"originPath,omitempty"`
			CachePaths string `json:"cachePaths,omitempty"` // DEPRECATED: use OriginPath
		}{DefaultTtl: ttl},
	}
}

func elasticacheCfg(nodeSize, storage, replicas string) *Config {
	return &Config{
		AWSElastiCache: &struct {
			HA       *bool  `json:"ha,omitempty"`
			Storage  string `json:"storageSize,omitempty"`
			NodeSize string `json:"nodeSize,omitempty"`
			Replicas string `json:"replicas,omitempty"`
		}{NodeSize: nodeSize, Storage: storage, Replicas: replicas},
	}
}

func rdsCfg(cpu, replicas, storage string) *Config {
	return &Config{
		AWSRDS: &struct {
			CPUSize      string `json:"cpuSize,omitempty"`
			ReadReplicas string `json:"readReplicas,omitempty"`
			StorageSize  string `json:"storageSize,omitempty"`
		}{CPUSize: cpu, ReadReplicas: replicas, StorageSize: storage},
	}
}

func cloudfrontCfg(ttl, originPath string) *Config {
	var ttlPtr, opPtr *string
	if ttl != "" {
		ttlPtr = &ttl
	}
	if originPath != "" {
		opPtr = &originPath
	}
	return &Config{
		AWSCloudfront: &struct {
			DefaultTtl *string `json:"defaultTtl,omitempty"`
			OriginPath *string `json:"originPath,omitempty"`
			CachePaths *string `json:"cachePaths,omitempty"` // DEPRECATED: use OriginPath
		}{DefaultTtl: ttlPtr, OriginPath: opPtr},
	}
}

func mskCfg(retention string) *Config {
	return &Config{
		AWSMSK: &struct {
			Retention string `json:"retentionPeriod,omitempty"`
		}{Retention: retention},
	}
}

func lambdaCfg(runtime, memory, timeout string) *Config {
	return &Config{
		AWSLambda: &struct {
			Runtime    string `json:"runtime,omitempty"`
			MemorySize string `json:"memorySize,omitempty"`
			Timeout    string `json:"timeout,omitempty"`
		}{Runtime: runtime, MemorySize: memory, Timeout: timeout},
	}
}

func assertValidationError(t *testing.T, err error) {
	t.Helper()
	require.Error(t, err)
	var ve *ValidationError
	assert.True(t, errors.As(err, &ve), "expected *ValidationError, got %T: %v", err, err)
}

// ---------------------------------------------------------------------------
// 1. DynamoDB billing_mode — must produce uppercase canonical token.
// ---------------------------------------------------------------------------

func TestBuildModuleValues_AWSDynamoDB_BillingMode(t *testing.T) {
	m := DefaultMapper{}

	cases := []struct {
		in   string
		want string
	}{
		// IR canonical enum literals
		{"On demand", "PAY_PER_REQUEST"},
		{"provisioned", "PROVISIONED"},
		// Case variants
		{"on demand", "PAY_PER_REQUEST"},
		{"On Demand", "PAY_PER_REQUEST"},
		{"PROVISIONED", "PROVISIONED"},
		{"Provisioned", "PROVISIONED"},
		// .or(ZNA) escape-hatch passthrough
		{"PAY_PER_REQUEST", "PAY_PER_REQUEST"},
		{"pay_per_request", "PAY_PER_REQUEST"},
		// Forgiving punctuation
		{"on-demand", "PAY_PER_REQUEST"},
		{"on_demand", "PAY_PER_REQUEST"},
		{"  On demand  ", "PAY_PER_REQUEST"},
	}

	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			vals, err := m.BuildModuleValues(KeyAWSDynamoDB, nil, ddbCfg(tc.in), "", "")
			require.NoError(t, err)
			assert.Equal(t, tc.want, vals["billing_mode"])
		})
	}

	t.Run("rejects garbage with ValidationError", func(t *testing.T) {
		_, err := m.BuildModuleValues(KeyAWSDynamoDB, nil, ddbCfg("free tier"), "", "")
		assertValidationError(t, err)
	})

	t.Run("empty Type is a no-op (module default wins)", func(t *testing.T) {
		vals, err := m.BuildModuleValues(KeyAWSDynamoDB, nil, ddbCfg(""), "", "")
		require.NoError(t, err)
		_, hasKey := vals["billing_mode"]
		assert.False(t, hasKey)
	})
}

// ---------------------------------------------------------------------------
// 2. OpenSearch storage_size — module declares string, strips "GB" before
//    tonumber(). "1TB" passed through verbatim broke that. Normalise to
//    "<N>GB" so the module's existing replace() works.
// ---------------------------------------------------------------------------

func TestBuildModuleValues_AWSOpenSearch_StorageSize(t *testing.T) {
	m := DefaultMapper{}

	cases := []struct {
		in   string
		want string
	}{
		{"10GB", "10GB"},
		{"100GB", "100GB"},
		{"1TB", "1000GB"},
		{"2TB", "2000GB"},
		{"  10GB  ", "10GB"},
		{"10gb", "10GB"}, // case insensitive
	}

	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			vals, err := m.BuildModuleValues(KeyAWSOpenSearch, &Components{}, openSearchCfg(tc.in), "", "")
			require.NoError(t, err)
			assert.Equal(t, tc.want, vals["storage_size"])
		})
	}

	t.Run("rejects garbage with ValidationError", func(t *testing.T) {
		_, err := m.BuildModuleValues(KeyAWSOpenSearch, &Components{}, openSearchCfg("huge"), "", "")
		assertValidationError(t, err)
	})
}

// ---------------------------------------------------------------------------
// 3. GCP CloudCDN default_ttl — module declares type = number; strings like
//    "1h" / "1day" rejected at plan time. Translate to seconds.
// ---------------------------------------------------------------------------

func TestBuildModuleValues_GCPCloudCDN_DefaultTTL(t *testing.T) {
	m := DefaultMapper{}

	cases := []struct {
		in   string
		want int
	}{
		{"0", 0},
		{"1h", 3600},
		{"1day", 86400},
		{"30s", 30},
		{"5m", 300},
		{"7days", 7 * 86400},
		{"3600", 3600}, // bare seconds passthrough
	}

	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			vals, err := m.BuildModuleValues(KeyGCPCloudCDN, nil, gcpCdnCfg(tc.in), "", "")
			require.NoError(t, err)
			assert.Equal(t, tc.want, vals["default_ttl"])
		})
	}

	t.Run("rejects garbage with ValidationError", func(t *testing.T) {
		_, err := m.BuildModuleValues(KeyGCPCloudCDN, nil, gcpCdnCfg("forever"), "", "")
		assertValidationError(t, err)
	})
}

// ---------------------------------------------------------------------------
// 4. ElastiCache replicas — module variable is type = number with
//    validation >= 0. IR enum embeds the integer in a label.
// ---------------------------------------------------------------------------

func TestBuildModuleValues_AWSElastiCache_Replicas(t *testing.T) {
	m := DefaultMapper{}

	cases := []struct {
		in   string
		want int
	}{
		{"0 read replicas", 0},
		{"1 read replica", 1},
		{"2 read replicas", 2},
		{"3", 3}, // bare integer passthrough
	}

	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			vals, err := m.BuildModuleValues(KeyAWSElastiCache, nil, elasticacheCfg("", "", tc.in), "", "")
			require.NoError(t, err)
			assert.Equal(t, tc.want, vals["replicas"])
		})
	}

	t.Run("rejects non-numeric replicas with ValidationError", func(t *testing.T) {
		_, err := m.BuildModuleValues(KeyAWSElastiCache, nil, elasticacheCfg("", "", "many"), "", "")
		assertValidationError(t, err)
	})
}

// ---------------------------------------------------------------------------
// 5. RDS keys — must match the module's actual variables.tf declarations.
//    instance_class / read_replica_count / allocated_storage. The old
//    node_cpu_size / num_read_nodes / storage_size keys must NOT be emitted.
// ---------------------------------------------------------------------------

func TestBuildModuleValues_AWSRDS_VariableNames(t *testing.T) {
	m := DefaultMapper{}

	t.Run("emits canonical instance_class / read_replica_count / allocated_storage", func(t *testing.T) {
		vals, err := m.BuildModuleValues(KeyAWSRDS, nil, rdsCfg("8 vCPU", "2 read replicas", "2TB"), "", "")
		require.NoError(t, err)
		assert.Equal(t, "db.m7i.2xlarge", vals["instance_class"])
		assert.Equal(t, 2, vals["read_replica_count"])
		assert.Equal(t, 2000, vals["allocated_storage"])
	})

	t.Run("does NOT emit pre-fix key names that the module never declared", func(t *testing.T) {
		vals, err := m.BuildModuleValues(KeyAWSRDS, nil, rdsCfg("4 vCPU", "1 read replica", "200GB"), "", "")
		require.NoError(t, err)
		_, hasOld1 := vals["node_cpu_size"]
		_, hasOld2 := vals["num_read_nodes"]
		// storage_size is the same name on both sides; in RDS we now emit
		// allocated_storage instead, so check that explicitly.
		assert.False(t, hasOld1, "node_cpu_size should not be emitted for RDS")
		assert.False(t, hasOld2, "num_read_nodes should not be emitted for RDS")
		assert.Equal(t, "db.m7i.xlarge", vals["instance_class"])
		assert.Equal(t, 1, vals["read_replica_count"])
		assert.Equal(t, 200, vals["allocated_storage"])
		_, hasStorageSize := vals["storage_size"]
		assert.False(t, hasStorageSize, "storage_size should not be emitted for RDS — module variable is allocated_storage")
	})

	t.Run("1 vCPU maps to db.t3.medium", func(t *testing.T) {
		vals, err := m.BuildModuleValues(KeyAWSRDS, nil, rdsCfg("1 vCPU", "", ""), "", "")
		require.NoError(t, err)
		assert.Equal(t, "db.t3.medium", vals["instance_class"])
	})

	t.Run("db.* passthrough (escape hatch)", func(t *testing.T) {
		vals, err := m.BuildModuleValues(KeyAWSRDS, nil, rdsCfg("db.r6g.4xlarge", "", ""), "", "")
		require.NoError(t, err)
		assert.Equal(t, "db.r6g.4xlarge", vals["instance_class"])
	})

	t.Run("rejects unknown CPU label with ValidationError", func(t *testing.T) {
		_, err := m.BuildModuleValues(KeyAWSRDS, nil, rdsCfg("16 vCPU", "", ""), "", "")
		assertValidationError(t, err)
	})
}

// ---------------------------------------------------------------------------
// 6. ElastiCache keys — node_type (not node_size); no storage_size.
// ---------------------------------------------------------------------------

func TestBuildModuleValues_AWSElastiCache_VariableNames(t *testing.T) {
	m := DefaultMapper{}

	t.Run("emits canonical node_type, drops orphan storage_size", func(t *testing.T) {
		vals, err := m.BuildModuleValues(KeyAWSElastiCache, nil, elasticacheCfg("8 vCPU", "20GB", "2 read replicas"), "", "")
		require.NoError(t, err)
		assert.Equal(t, "cache.r6g.2xlarge", vals["node_type"])
		_, hasNodeSize := vals["node_size"]
		assert.False(t, hasNodeSize, "node_size should not be emitted — module variable is node_type")
		_, hasStorage := vals["storage_size"]
		assert.False(t, hasStorage, "storage_size should not be emitted — Redis is not capacity-priced")
		assert.Equal(t, 2, vals["replicas"])
	})

	t.Run("1 vCPU maps to cache.t3.medium", func(t *testing.T) {
		vals, err := m.BuildModuleValues(KeyAWSElastiCache, nil, elasticacheCfg("1 vCPU", "", ""), "", "")
		require.NoError(t, err)
		assert.Equal(t, "cache.t3.medium", vals["node_type"])
	})

	t.Run("4 vCPU maps to cache.r6g.xlarge", func(t *testing.T) {
		vals, err := m.BuildModuleValues(KeyAWSElastiCache, nil, elasticacheCfg("4 vCPU", "", ""), "", "")
		require.NoError(t, err)
		assert.Equal(t, "cache.r6g.xlarge", vals["node_type"])
	})

	t.Run("cache.* passthrough", func(t *testing.T) {
		vals, err := m.BuildModuleValues(KeyAWSElastiCache, nil, elasticacheCfg("cache.m6g.large", "", ""), "", "")
		require.NoError(t, err)
		assert.Equal(t, "cache.m6g.large", vals["node_type"])
	})
}

// ---------------------------------------------------------------------------
// 7. CloudFront — module variable is default_ttl_seconds (number), not
//    default_ttl (string). origin_path is unchanged.
// ---------------------------------------------------------------------------

func TestBuildModuleValues_AWSCloudfront_VariableNames(t *testing.T) {
	m := DefaultMapper{}

	t.Run("default_ttl translates to default_ttl_seconds (number)", func(t *testing.T) {
		vals, err := m.BuildModuleValues(KeyAWSCloudfront, nil, cloudfrontCfg("1h", "/v1"), "", "")
		require.NoError(t, err)
		assert.Equal(t, 3600, vals["default_ttl_seconds"])
		_, hasOld := vals["default_ttl"]
		assert.False(t, hasOld, "default_ttl (string) should not be emitted — module variable is default_ttl_seconds")
		assert.Equal(t, "/v1", vals["origin_path"])
	})

	t.Run("0 stays 0 seconds", func(t *testing.T) {
		vals, err := m.BuildModuleValues(KeyAWSCloudfront, nil, cloudfrontCfg("0", ""), "", "")
		require.NoError(t, err)
		assert.Equal(t, 0, vals["default_ttl_seconds"])
	})

	t.Run("1day → 86400", func(t *testing.T) {
		vals, err := m.BuildModuleValues(KeyAWSCloudfront, nil, cloudfrontCfg("1day", ""), "", "")
		require.NoError(t, err)
		assert.Equal(t, 86400, vals["default_ttl_seconds"])
	})

	t.Run("rejects garbage TTL with ValidationError", func(t *testing.T) {
		_, err := m.BuildModuleValues(KeyAWSCloudfront, nil, cloudfrontCfg("forever", ""), "", "")
		assertValidationError(t, err)
	})
}

// ---------------------------------------------------------------------------
// 8. MSK — module now declares retention_hours; mapper translates the IR
//    enum and emits under that key (was retention_period, undeclared).
// ---------------------------------------------------------------------------

func TestBuildModuleValues_AWSMSK_RetentionHours(t *testing.T) {
	m := DefaultMapper{}

	cases := []struct {
		in   string
		want int
	}{
		{"3 days", 72},
		{"7 days", 168},
		{"14 days", 336},
		{"168h", 168}, // escape-hatch passthrough
		{"7d", 168},
		{"168", 168}, // bare integer = hours
	}

	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			vals, err := m.BuildModuleValues(KeyAWSMSK, nil, mskCfg(tc.in), "", "")
			require.NoError(t, err)
			assert.Equal(t, tc.want, vals["retention_hours"])
			_, hasOld := vals["retention_period"]
			assert.False(t, hasOld, "retention_period should not be emitted — module variable is retention_hours")
		})
	}

	t.Run("rejects garbage with ValidationError", func(t *testing.T) {
		_, err := m.BuildModuleValues(KeyAWSMSK, nil, mskCfg("forever"), "", "")
		assertValidationError(t, err)
	})
}

// ---------------------------------------------------------------------------
// 8b. SQS — bonus finding the subset-check test surfaced beyond the audit:
//     mapper emitted `type` / `visibility_timeout`; module variables are
//     `queue_type` / `visibility_timeout_seconds`.
// ---------------------------------------------------------------------------

func sqsCfg(typ, visTimeout string) *Config {
	return &Config{
		AWSSQS: &struct {
			Type              string `json:"type,omitempty"`
			VisibilityTimeout string `json:"visibilityTimeout,omitempty"`
		}{Type: typ, VisibilityTimeout: visTimeout},
	}
}

func TestBuildModuleValues_AWSSQS_VariableNames(t *testing.T) {
	m := DefaultMapper{}

	t.Run("emits queue_type / visibility_timeout_seconds (not type / visibility_timeout)", func(t *testing.T) {
		vals, err := m.BuildModuleValues(KeyAWSSQS, nil, sqsCfg("FIFO", "600"), "", "")
		require.NoError(t, err)
		assert.Equal(t, "FIFO", vals["queue_type"])
		assert.Equal(t, 600, vals["visibility_timeout_seconds"])
		_, hasOldType := vals["type"]
		_, hasOldVis := vals["visibility_timeout"]
		assert.False(t, hasOldType, "type is the pre-fix key — module variable is queue_type")
		assert.False(t, hasOldVis, "visibility_timeout is the pre-fix key — module variable is visibility_timeout_seconds")
	})

	t.Run("VisibilityTimeout supports duration suffix", func(t *testing.T) {
		vals, err := m.BuildModuleValues(KeyAWSSQS, nil, sqsCfg("Standard", "10m"), "", "")
		require.NoError(t, err)
		assert.Equal(t, 600, vals["visibility_timeout_seconds"])
	})

	t.Run("rejects garbage VisibilityTimeout with ValidationError", func(t *testing.T) {
		_, err := m.BuildModuleValues(KeyAWSSQS, nil, sqsCfg("Standard", "soon"), "", "")
		assertValidationError(t, err)
	})
}

// ---------------------------------------------------------------------------
// 9. Lambda — strict parsers; loud failure on unrecognised input.
// ---------------------------------------------------------------------------

func TestBuildModuleValues_AWSLambda_MemoryAndTimeout(t *testing.T) {
	m := DefaultMapper{}

	t.Run("happy path: IR enum values translate", func(t *testing.T) {
		cases := []struct {
			memory string
			tmout  string
			wantM  int
			wantT  int
		}{
			{"128", "3s", 128, 3},
			{"512", "30s", 512, 30},
			{"1024", "15m", 1024, 900},
			{"3072", "1h", 3072, 3600},
		}
		for _, tc := range cases {
			t.Run(tc.memory+"/"+tc.tmout, func(t *testing.T) {
				vals, err := m.BuildModuleValues(KeyAWSLambda, nil, lambdaCfg("", tc.memory, tc.tmout), "", "")
				require.NoError(t, err)
				assert.Equal(t, tc.wantM, vals["memory_size"])
				assert.Equal(t, tc.wantT, vals["timeout"])
			})
		}
	})

	t.Run("rejects non-integer memory_size with ValidationError", func(t *testing.T) {
		// Previously silently dropped — module default would have won.
		_, err := m.BuildModuleValues(KeyAWSLambda, nil, lambdaCfg("", "512MB", ""), "", "")
		assertValidationError(t, err)
	})

	t.Run("rejects bare-integer timeout (no unit) with ValidationError", func(t *testing.T) {
		_, err := m.BuildModuleValues(KeyAWSLambda, nil, lambdaCfg("", "", "30"), "", "")
		assertValidationError(t, err)
	})

	t.Run("rejects unknown timeout unit with ValidationError", func(t *testing.T) {
		_, err := m.BuildModuleValues(KeyAWSLambda, nil, lambdaCfg("", "", "5y"), "", "")
		assertValidationError(t, err)
	})

	t.Run("runtime defaults still apply when memory/timeout absent", func(t *testing.T) {
		vals, err := m.BuildModuleValues(KeyAWSLambda, nil, lambdaCfg("", "", ""), "", "")
		require.NoError(t, err)
		assert.Equal(t, "nodejs20.x", vals["runtime"])
		_, hasMem := vals["memory_size"]
		_, hasTmout := vals["timeout"]
		assert.False(t, hasMem)
		assert.False(t, hasTmout)
	})
}

// ---------------------------------------------------------------------------
// 10–12. Audit-class follow-ups surfaced when the subset test was refactored
//        to range over the full ComponentKey registry instead of a static
//        13-entry allowlist. Each is the same shape as findings 5–8: mapper
//        emitted under a key the module never declared, so the value was
//        silently dropped at compose time.
// ---------------------------------------------------------------------------

// 10. GCP Cloud KMS — module variable is `keyring_name` (one word), not
//     `key_ring_name` (two). Pre-fix mapper emitted the latter, silently
//     dropped, and the module default "main" always won.
func TestBuildModuleValues_GCPCloudKMS_KeyringName(t *testing.T) {
	m := DefaultMapper{}

	vals, err := m.BuildModuleValues(KeyGCPCloudKMS, nil, nil, "", "")
	require.NoError(t, err)
	assert.Equal(t, "main-keyring", vals["keyring_name"])
	_, hasOld := vals["key_ring_name"]
	assert.False(t, hasOld, "key_ring_name is the pre-fix key — module variable is keyring_name")
}

// 11. GCP Secret Manager — module variable is `secrets` (list of objects)
//     with default `[]`. Pre-fix mapper emitted `secret_id = "main-secret"`
//     against a non-existent variable; value silently dropped. Drop the
//     orphan emission so the mapper's output is honest about what it can
//     actually configure.
func TestBuildModuleValues_GCPSecretManager_NoOrphanSecretID(t *testing.T) {
	m := DefaultMapper{}

	vals, err := m.BuildModuleValues(KeyGCPSecretManager, nil, nil, "", "")
	require.NoError(t, err)
	_, hasOld := vals["secret_id"]
	assert.False(t, hasOld, "secret_id is the pre-fix orphan key — gcp/secretmanager declares `secrets` (list); leave it to user tfvars")
}

// 12. GCP Firestore — module declares only project/region; the (default)
//     database is created implicitly. Pre-fix mapper emitted
//     `database_id = "(default)"` against a non-existent variable.
func TestBuildModuleValues_GCPFirestore_NoOrphanDatabaseID(t *testing.T) {
	m := DefaultMapper{}

	vals, err := m.BuildModuleValues(KeyGCPFirestore, nil, nil, "", "")
	require.NoError(t, err)
	_, hasOld := vals["database_id"]
	assert.False(t, hasOld, "database_id is the pre-fix orphan key — gcp/firestore declares no such variable")
}

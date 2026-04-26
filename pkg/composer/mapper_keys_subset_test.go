package composer

// TestMapperKeysSubsetOfModuleVariables is the generic safety net for the
// upstream issue #131 audit — it verifies that every key the mapper writes
// for a given component is a declared variable in that module's
// variables.tf. The existing TestComposeStack_TFVarsMatchVariables only
// checks the *root* variables.tf the composer assembles, which means it
// can't catch mapper bugs where compose.go silently filters out tfvars
// whose key isn't a declared module variable (the most common shape of
// audit findings 5–8).
//
// Adding a new mapper case that writes a key the target module didn't
// declare will fail this test. Renaming a module variable upstream
// without updating the mapper will fail this test.

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// kitchenSinkConfig populates every cfg sub-struct the mapper reads with
// values that exercise each mapper branch. Used to drive a single mapper
// invocation per ComponentKey for the cross-module check below.
func kitchenSinkConfig() *Config {
	ttl := "1h"
	op := "/v1"
	t := true

	return &Config{
		Cloud:  "aws",
		Region: "us-east-1",
		AWSCloudfront: &struct {
			DefaultTtl *string `json:"defaultTtl,omitempty"`
			OriginPath *string `json:"originPath,omitempty"`
			CachePaths *string `json:"cachePaths,omitempty"` // DEPRECATED: use OriginPath
		}{DefaultTtl: &ttl, OriginPath: &op},
		AWSRDS: &struct {
			CPUSize      string `json:"cpuSize,omitempty"`
			ReadReplicas string `json:"readReplicas,omitempty"`
			StorageSize  string `json:"storageSize,omitempty"`
		}{CPUSize: "8 vCPU", ReadReplicas: "2 read replicas", StorageSize: "200GB"},
		AWSElastiCache: &struct {
			HA       *bool  `json:"ha,omitempty"`
			Storage  string `json:"storageSize,omitempty"`
			NodeSize string `json:"nodeSize,omitempty"`
			Replicas string `json:"replicas,omitempty"`
		}{HA: &t, Storage: "20GB", NodeSize: "8 vCPU", Replicas: "2 read replicas"},
		AWSS3: &struct {
			Versioning *bool `json:"versioning,omitempty"`
		}{Versioning: &t},
		AWSDynamoDB: &struct {
			Type string `json:"type,omitempty"`
		}{Type: "On demand"},
		AWSSQS: &struct {
			Type              string `json:"type,omitempty"`
			VisibilityTimeout string `json:"visibilityTimeout,omitempty"`
		}{Type: "FIFO", VisibilityTimeout: "600"},
		AWSMSK: &struct {
			Retention string `json:"retentionPeriod,omitempty"`
		}{Retention: "7 days"},
		AWSCloudWatchLogs: &struct {
			RetentionDays int `json:"retentionDays,omitempty"`
		}{RetentionDays: 90},
		AWSLambda: &struct {
			Runtime    string `json:"runtime,omitempty"`
			MemorySize string `json:"memorySize,omitempty"`
			Timeout    string `json:"timeout,omitempty"`
		}{Runtime: "nodejs20.x", MemorySize: "512", Timeout: "30s"},
		AWSOpenSearch: &struct {
			DeploymentType string `json:"deploymentType,omitempty"`
			InstanceType   string `json:"instanceType,omitempty"`
			StorageSize    string `json:"storageSize,omitempty"`
			MultiAZ        *bool  `json:"multiAz,omitempty"`
		}{DeploymentType: "managed", InstanceType: "t3.medium.search", StorageSize: "1TB", MultiAZ: &t},
		AWSKMS: &struct {
			NumKeys string `json:"numKeys,omitempty"`
		}{NumKeys: "1"},
		AWSSecretsManager: &struct {
			NumSecrets string `json:"numSecrets,omitempty"`
		}{NumSecrets: "1"},
		GCPCloudCDN: &struct {
			DefaultTtl string `json:"defaultTtl,omitempty"`
			OriginPath string `json:"originPath,omitempty"`
			CachePaths string `json:"cachePaths,omitempty"` // DEPRECATED: use OriginPath
		}{DefaultTtl: "1h"},
	}
}

// keysToCheck enumerates the ComponentKeys whose mapper cases write
// component-specific tfvars (i.e., where a stale key name silently
// drops the user's choice). Common keys (project / region / environment)
// and preview-stub keys (vpc_id / subnet_ids / cluster_name) are read
// from this same list because every module that uses them declares them.
//
// Each entry is paired with the cloud prefix used by GetPresetPath.
var mapperKeyCheckTargets = []struct {
	key   ComponentKey
	cloud string
}{
	{KeyAWSDynamoDB, "aws"},
	{KeyAWSOpenSearch, "aws"},
	{KeyAWSElastiCache, "aws"},
	{KeyAWSRDS, "aws"},
	{KeyAWSCloudfront, "aws"},
	{KeyAWSMSK, "aws"},
	{KeyAWSLambda, "aws"},
	{KeyAWSSQS, "aws"},
	{KeyAWSCloudWatchLogs, "aws"},
	{KeyAWSKMS, "aws"},
	{KeyAWSSecretsManager, "aws"},
	{KeyAWSS3, "aws"},
	{KeyGCPCloudCDN, "gcp"},
}

func TestMapperKeysSubsetOfModuleVariables(t *testing.T) {
	m := DefaultMapper{}
	cfg := kitchenSinkConfig()
	c := newTestClient()

	varDeclRe := regexp.MustCompile(`variable\s+"([^"]+)"`)

	for _, tt := range mapperKeyCheckTargets {
		t.Run(string(tt.key), func(t *testing.T) {
			vals, err := m.BuildModuleValues(tt.key, &Components{}, cfg, "test", "us-east-1")
			require.NoError(t, err, "mapper should not fail with the kitchen-sink config")

			presetPath := GetPresetPath(tt.cloud, tt.key, &Components{})
			files, err := c.GetPresetFiles(presetPath)
			require.NoError(t, err, "GetPresetFiles(%s)", presetPath)
			varsTF, ok := files["/variables.tf"]
			require.True(t, ok, "%s should have a /variables.tf", presetPath)

			declared := map[string]bool{}
			for _, m := range varDeclRe.FindAllStringSubmatch(string(varsTF), -1) {
				declared[m[1]] = true
			}

			// Common keys that DefaultMapper unconditionally sets for every
			// component. AWS modules consistently declare all three; some
			// GCP modules don't declare environment yet (tracked separately
			// from this audit). The mismatch isn't an audit-class user-data
			// bug — it's a metadata default that just gets dropped — so
			// exempt these from the strict subset check.
			commonDefaults := map[string]bool{
				"project":     true,
				"region":      true,
				"environment": true,
			}

			for k := range vals {
				if commonDefaults[k] {
					continue
				}
				assert.True(t, declared[k],
					"mapper for %s emits key %q which is not declared in %s/variables.tf — declared: %v",
					tt.key, k, presetPath, sortedKeys(declared))
			}
		})
	}
}

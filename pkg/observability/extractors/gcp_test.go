// Per-extractor field-coverage tests for every GCP extractor in
// extractors.go. Closes the GCP half of Gap 1 of issue #236 — the drift
// suite (extractors_drift_test.go) pins exact field counts and dispatch
// wiring, but does not exercise per-branch logic. This file does.
//
// Style mirrors the AWS counterpart (aws_test.go): each top-level
// TestExtractGCP<Name>Config exercises happy path + nil/empty + envelope
// variants + any branch logic the extractor actually implements. Where
// the issue spec asks for a branch the source code does NOT yet
// implement (e.g. GCS lifecycle policy, VPC IGW classification, VPC
// auto-mode keyed as `vpcMode`), the test asserts the CURRENT behaviour
// (the field is simply absent) so future implementation work has a
// failing assertion to flip.
package extractors

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------- gcp_compute ----------

func TestExtractGCPComputeConfig(t *testing.T) {
	t.Parallel()

	t.Run("HappyPath", func(t *testing.T) {
		t.Parallel()
		raw := []any{
			map[string]any{
				"name":        "demo-vm",
				"machineType": "https://www.googleapis.com/compute/v1/projects/demo/zones/us-central1-a/machineTypes/e2-medium",
				"zone":        "https://www.googleapis.com/compute/v1/projects/demo/zones/us-central1-a",
				"status":      "RUNNING",
			},
			map[string]any{"name": "demo-vm-2", "status": "STOPPED"},
		}
		got := extractGCPComputeConfig(raw)
		require.NotNil(t, got)
		assert.Equal(t, "2", got["instanceCount"])
		assert.Equal(t, "demo-vm", got["instanceName"])
		assert.Equal(t, "e2-medium", got["machineType"])
		assert.Equal(t, "us-central1-a", got["zone"])
		assert.Equal(t, "RUNNING", got["status"])
	})

	t.Run("Envelope", func(t *testing.T) {
		t.Parallel()
		raw := map[string]any{
			"instances": []any{
				map[string]any{"name": "vm-1", "status": "RUNNING"},
			},
		}
		got := extractGCPComputeConfig(raw)
		require.NotNil(t, got)
		assert.Equal(t, "1", got["instanceCount"])
		assert.Equal(t, "vm-1", got["instanceName"])
		assert.Equal(t, "RUNNING", got["status"])
	})

	t.Run("BasenameOnURLs", func(t *testing.T) {
		t.Parallel()
		// Confirms the extractor strips the full GCP resource URL down to
		// the trailing segment (basename), which is what the UI compares.
		raw := []any{map[string]any{
			"name":        "vm",
			"machineType": "projects/p/zones/us-central1-a/machineTypes/n2-standard-4",
			"zone":        "projects/p/zones/us-central1-a",
		}}
		got := extractGCPComputeConfig(raw)
		assert.Equal(t, "n2-standard-4", got["machineType"])
		assert.Equal(t, "us-central1-a", got["zone"])
	})

	t.Run("NilInput", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractGCPComputeConfig(nil))
	})

	t.Run("EmptyList", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractGCPComputeConfig([]any{}))
	})

	t.Run("EmptyEnvelope", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractGCPComputeConfig(map[string]any{"instances": []any{}}))
	})
}

// ---------- gcp_gke ----------

func TestExtractGCPGKEConfig(t *testing.T) {
	t.Parallel()

	t.Run("HappyPath", func(t *testing.T) {
		t.Parallel()
		raw := []any{map[string]any{
			"name":                 "demo-gke",
			"status":               "RUNNING",
			"location":             "us-central1",
			"currentNodeCount":     float64(3),
			"currentMasterVersion": "1.29.4-gke.1043000",
			"autopilot":            map[string]any{"enabled": true},
			"privateClusterConfig": map[string]any{"enablePrivateNodes": true},
		}}
		got := extractGCPGKEConfig(raw)
		require.NotNil(t, got)
		assert.Equal(t, "1", got["clusterCount"])
		assert.Equal(t, "demo-gke", got["clusterName"])
		assert.Equal(t, "RUNNING", got["status"])
		assert.Equal(t, "us-central1", got["location"])
		assert.Equal(t, "3", got["nodeCount"])
		assert.Equal(t, "1.29.4-gke.1043000", got["clusterVersion"])
		assert.Equal(t, "Yes", got["autopilot"])
		assert.Equal(t, "Yes", got["privateCluster"])
	})

	t.Run("AutopilotDisabled", func(t *testing.T) {
		t.Parallel()
		raw := []any{map[string]any{
			"name":      "std-gke",
			"autopilot": map[string]any{"enabled": false},
			"privateClusterConfig": map[string]any{
				"enablePrivateNodes": false,
			},
		}}
		got := extractGCPGKEConfig(raw)
		assert.Equal(t, "No", got["autopilot"])
		assert.Equal(t, "No", got["privateCluster"])
	})

	t.Run("NoSubObjects", func(t *testing.T) {
		t.Parallel()
		raw := []any{map[string]any{"name": "bare"}}
		got := extractGCPGKEConfig(raw)
		require.NotNil(t, got)
		assert.NotContains(t, got, "autopilot")
		assert.NotContains(t, got, "privateCluster")
	})

	t.Run("Envelope", func(t *testing.T) {
		t.Parallel()
		raw := map[string]any{"clusters": []any{
			map[string]any{"name": "wrap-gke", "status": "PROVISIONING"},
		}}
		got := extractGCPGKEConfig(raw)
		assert.Equal(t, "wrap-gke", got["clusterName"])
		assert.Equal(t, "PROVISIONING", got["status"])
	})

	t.Run("Empty", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractGCPGKEConfig(nil))
		assert.Nil(t, extractGCPGKEConfig([]any{}))
	})
}

// ---------- gcp_cloud_run ----------

func TestExtractGCPCloudRunConfig(t *testing.T) {
	t.Parallel()

	t.Run("HappyPath", func(t *testing.T) {
		t.Parallel()
		raw := []any{map[string]any{
			"name": "projects/demo/locations/us-central1/services/svc-1",
			"uri":  "https://svc-1-abc-uc.a.run.app",
			"template": map[string]any{
				"containers": []any{map[string]any{
					"resources": map[string]any{
						"limits": map[string]any{"cpu": "2", "memory": "1Gi"},
					},
				}},
				"scaling": map[string]any{
					"minInstanceCount": float64(1),
					"maxInstanceCount": float64(20),
				},
				"maxInstanceRequestConcurrency": float64(80),
			},
		}}
		got := extractGCPCloudRunConfig(raw)
		require.NotNil(t, got)
		assert.Equal(t, "1", got["serviceCount"])
		assert.Equal(t, "svc-1", got["serviceName"]) // basename stripped
		assert.Equal(t, "https://svc-1-abc-uc.a.run.app", got["uri"])
		assert.Equal(t, "us-central1", got["location"])
		assert.Equal(t, "2", got["cpu"])
		assert.Equal(t, "1Gi", got["memory"])
		assert.Equal(t, "1", got["minInstances"])
		assert.Equal(t, "20", got["maxInstances"])
		assert.Equal(t, "80", got["concurrency"])
	})

	t.Run("BasenameStripping", func(t *testing.T) {
		t.Parallel()
		raw := []any{map[string]any{
			"name": "projects/foo/locations/europe-west1/services/api",
		}}
		got := extractGCPCloudRunConfig(raw)
		assert.Equal(t, "api", got["serviceName"])
		assert.Equal(t, "europe-west1", got["location"])
	})

	t.Run("ShortName", func(t *testing.T) {
		t.Parallel()
		// gcpResourceBasename returns the input unchanged if no slash.
		raw := []any{map[string]any{"name": "plain-name"}}
		got := extractGCPCloudRunConfig(raw)
		assert.Equal(t, "plain-name", got["serviceName"])
		assert.NotContains(t, got, "location")
	})

	t.Run("NoTemplate", func(t *testing.T) {
		t.Parallel()
		raw := []any{map[string]any{"name": "projects/p/locations/us-east1/services/s"}}
		got := extractGCPCloudRunConfig(raw)
		assert.NotContains(t, got, "cpu")
		assert.NotContains(t, got, "memory")
		assert.NotContains(t, got, "minInstances")
		assert.NotContains(t, got, "concurrency")
	})

	t.Run("Envelope", func(t *testing.T) {
		t.Parallel()
		raw := map[string]any{"services": []any{
			map[string]any{"name": "projects/p/locations/us/services/x"},
		}}
		got := extractGCPCloudRunConfig(raw)
		assert.Equal(t, "x", got["serviceName"])
	})

	t.Run("Empty", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractGCPCloudRunConfig(nil))
		assert.Nil(t, extractGCPCloudRunConfig([]any{}))
	})
}

// ---------- extractGCPLocationFromName ----------

func TestExtractGCPLocationFromName(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		out  string
	}{
		{"FullCloudRunName", "projects/demo/locations/us-central1/services/svc", "us-central1"},
		{"VertexEndpoint", "projects/p/locations/europe-west4/endpoints/123", "europe-west4"},
		{"NoLocations", "projects/p/topics/x", ""},
		{"Empty", "", ""},
		{"TrailingLocation", "projects/p/locations/us-east1", "us-east1"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.out, extractGCPLocationFromName(tc.in))
		})
	}
}

// ---------- gcp_memorystore ----------

func TestExtractGCPMemorystoreConfig(t *testing.T) {
	t.Parallel()

	t.Run("HappyPath", func(t *testing.T) {
		t.Parallel()
		raw := []any{map[string]any{
			"name":         "projects/demo/locations/us-central1/instances/redis-1",
			"tier":         "STANDARD_HA",
			"memorySizeGb": float64(5),
			"redisVersion": "REDIS_7_0",
			"state":        "READY",
			"locationId":   "us-central1-a",
		}}
		got := extractGCPMemorystoreConfig(raw)
		require.NotNil(t, got)
		assert.Equal(t, "1", got["instanceCount"])
		assert.Equal(t, "redis-1", got["instanceName"])
		assert.Equal(t, "STANDARD_HA", got["tier"])
		assert.Equal(t, "5", got["memorySizeGb"])
		assert.Equal(t, "REDIS_7_0", got["redisVersion"])
		assert.Equal(t, "READY", got["state"])
		assert.Equal(t, "us-central1-a", got["location"])
	})

	t.Run("Envelope", func(t *testing.T) {
		t.Parallel()
		raw := map[string]any{"instances": []any{
			map[string]any{"name": "x", "tier": "BASIC"},
		}}
		got := extractGCPMemorystoreConfig(raw)
		assert.Equal(t, "BASIC", got["tier"])
	})

	t.Run("Empty", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractGCPMemorystoreConfig(nil))
		assert.Nil(t, extractGCPMemorystoreConfig([]any{}))
	})
}

// ---------- gcp_cloudsql ----------

func TestExtractGCPCloudSQLConfig(t *testing.T) {
	t.Parallel()

	t.Run("HappyPath_HA_REGIONAL", func(t *testing.T) {
		t.Parallel()
		raw := map[string]any{"items": []any{map[string]any{
			"name":            "demo-pg",
			"databaseVersion": "POSTGRES_15",
			"state":           "RUNNABLE",
			"region":          "us-central1",
			"settings": map[string]any{
				"tier":             "db-custom-2-7680",
				"dataDiskSizeGb":   float64(50),
				"availabilityType": "REGIONAL",
				"backupConfiguration": map[string]any{
					"enabled": true,
				},
			},
		}}}
		got := extractGCPCloudSQLConfig(raw)
		require.NotNil(t, got)
		assert.Equal(t, "1", got["instanceCount"])
		assert.Equal(t, "demo-pg", got["instanceName"])
		assert.Equal(t, "POSTGRES_15", got["databaseVersion"])
		assert.Equal(t, "RUNNABLE", got["state"])
		assert.Equal(t, "us-central1", got["region"])
		assert.Equal(t, "db-custom-2-7680", got["tier"])
		assert.Equal(t, "50", got["diskSizeGb"])
		assert.Equal(t, "Yes", got["highAvailability"])
	})

	t.Run("HABranch_ZONAL", func(t *testing.T) {
		t.Parallel()
		raw := map[string]any{"items": []any{map[string]any{
			"settings": map[string]any{"availabilityType": "ZONAL"},
		}}}
		got := extractGCPCloudSQLConfig(raw)
		assert.Equal(t, "No", got["highAvailability"])
	})

	t.Run("HABranch_Unspecified", func(t *testing.T) {
		t.Parallel()
		// Neither REGIONAL nor ZONAL → field is absent (extractor's
		// switch has no default case for it).
		raw := map[string]any{"items": []any{map[string]any{
			"settings": map[string]any{"availabilityType": ""},
		}}}
		got := extractGCPCloudSQLConfig(raw)
		assert.NotContains(t, got, "highAvailability")
	})

	t.Run("BackupConfig_NoEffect", func(t *testing.T) {
		t.Parallel()
		// The current extractor reads availabilityType but does NOT
		// surface backupConfiguration.enabled into the output. Test
		// confirms current behavior — flip this when issue #236
		// follow-up wires it through.
		rawEnabled := map[string]any{"items": []any{map[string]any{
			"settings": map[string]any{
				"backupConfiguration": map[string]any{"enabled": true},
				"availabilityType":    "REGIONAL",
			},
		}}}
		gotEnabled := extractGCPCloudSQLConfig(rawEnabled)
		assert.NotContains(t, gotEnabled, "backupEnabled")

		rawDisabled := map[string]any{"items": []any{map[string]any{
			"settings": map[string]any{
				"backupConfiguration": map[string]any{"enabled": false},
				"availabilityType":    "ZONAL",
			},
		}}}
		gotDisabled := extractGCPCloudSQLConfig(rawDisabled)
		assert.NotContains(t, gotDisabled, "backupEnabled")
	})

	t.Run("DirectSlice", func(t *testing.T) {
		t.Parallel()
		raw := []any{map[string]any{
			"name":            "direct-pg",
			"databaseVersion": "POSTGRES_14",
		}}
		got := extractGCPCloudSQLConfig(raw)
		assert.Equal(t, "direct-pg", got["instanceName"])
		assert.Equal(t, "POSTGRES_14", got["databaseVersion"])
	})

	t.Run("NoSettings", func(t *testing.T) {
		t.Parallel()
		raw := map[string]any{"items": []any{map[string]any{"name": "bare"}}}
		got := extractGCPCloudSQLConfig(raw)
		assert.NotContains(t, got, "tier")
		assert.NotContains(t, got, "highAvailability")
	})

	t.Run("Empty", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractGCPCloudSQLConfig(nil))
		assert.Nil(t, extractGCPCloudSQLConfig(map[string]any{"items": []any{}}))
	})
}

// ---------- gcp_gcs ----------

func TestExtractGCPGCSConfig(t *testing.T) {
	t.Parallel()

	t.Run("HappyPath_Regional", func(t *testing.T) {
		t.Parallel()
		raw := []any{map[string]any{
			"name":         "demo-bucket",
			"location":     "us-central1",
			"storageClass": "STANDARD",
		}}
		got := extractGCPGCSConfig(raw)
		require.NotNil(t, got)
		assert.Equal(t, "1", got["bucketCount"])
		assert.Equal(t, "demo-bucket", got["bucketName"])
		assert.Equal(t, "us-central1", got["location"])
		assert.Equal(t, "STANDARD", got["storageClass"])
	})

	t.Run("MultiRegionalLocation", func(t *testing.T) {
		t.Parallel()
		// `US`, `EU`, `ASIA` are GCS multi-region locations. The current
		// extractor surfaces them as-is (no split into single-region vs
		// multi-region classification). Test asserts pass-through.
		raw := []any{map[string]any{
			"name":         "global-bucket",
			"location":     "US",
			"storageClass": "STANDARD",
		}}
		got := extractGCPGCSConfig(raw)
		assert.Equal(t, "US", got["location"])
	})

	t.Run("LifecycleNotSurfaced", func(t *testing.T) {
		t.Parallel()
		// Issue #236 spec calls for a `lifecyclePolicy=enabled` branch
		// when `lifecycle.rule` is present, but the inspector
		// pre-flattens to {name, location, storageClass, created} so
		// the extractor has no lifecycle field to read. Test asserts
		// current behavior — flip when the inspector surface widens.
		rawWithLifecycle := []any{map[string]any{
			"name":         "lc-bucket",
			"location":     "us-east1",
			"storageClass": "NEARLINE",
			"lifecycle":    map[string]any{"rule": []any{map[string]any{"action": map[string]any{"type": "Delete"}}}},
		}}
		got := extractGCPGCSConfig(rawWithLifecycle)
		assert.NotContains(t, got, "lifecyclePolicy")

		rawAbsent := []any{map[string]any{
			"name":         "plain-bucket",
			"location":     "us-east1",
			"storageClass": "NEARLINE",
		}}
		gotAbsent := extractGCPGCSConfig(rawAbsent)
		assert.NotContains(t, gotAbsent, "lifecyclePolicy")
	})

	t.Run("Envelope", func(t *testing.T) {
		t.Parallel()
		raw := map[string]any{"buckets": []any{
			map[string]any{"name": "env-bucket", "location": "EU"},
		}}
		got := extractGCPGCSConfig(raw)
		assert.Equal(t, "env-bucket", got["bucketName"])
		assert.Equal(t, "EU", got["location"])
	})

	t.Run("Empty", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractGCPGCSConfig(nil))
		assert.Nil(t, extractGCPGCSConfig([]any{}))
	})
}

// ---------- gcp_firestore ----------

func TestExtractGCPFirestoreConfig(t *testing.T) {
	t.Parallel()

	t.Run("HappyPath", func(t *testing.T) {
		t.Parallel()
		raw := []any{"users", "orders", "audit"}
		got := extractGCPFirestoreConfig(raw)
		require.NotNil(t, got)
		assert.Equal(t, "3", got["collectionCount"])
		assert.Equal(t, "users", got["collectionName"])
	})

	t.Run("Envelope", func(t *testing.T) {
		t.Parallel()
		raw := map[string]any{"collections": []any{"orders"}}
		got := extractGCPFirestoreConfig(raw)
		assert.Equal(t, "1", got["collectionCount"])
		assert.Equal(t, "orders", got["collectionName"])
	})

	t.Run("EmptyName", func(t *testing.T) {
		t.Parallel()
		raw := []any{""}
		got := extractGCPFirestoreConfig(raw)
		assert.Equal(t, "1", got["collectionCount"])
		assert.NotContains(t, got, "collectionName")
	})

	t.Run("Empty", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractGCPFirestoreConfig(nil))
		assert.Nil(t, extractGCPFirestoreConfig([]any{}))
	})
}

// ---------- gcp_pubsub ----------

func TestExtractGCPPubSubConfig(t *testing.T) {
	t.Parallel()

	t.Run("HappyPath", func(t *testing.T) {
		t.Parallel()
		raw := []any{map[string]any{
			"name":                     "projects/demo/topics/events",
			"messageRetentionDuration": "604800s",
			"kmsKeyName":               "projects/demo/locations/global/keyRings/r/cryptoKeys/k",
		}}
		got := extractGCPPubSubConfig(raw)
		require.NotNil(t, got)
		assert.Equal(t, "1", got["topicCount"])
		assert.Equal(t, "events", got["topicName"]) // basename
		assert.Equal(t, "604800s", got["messageRetentionDuration"])
		// kmsKeyName is read with getString (not gcpResourceBasename) so
		// the full path passes through unchanged.
		assert.Equal(t, "projects/demo/locations/global/keyRings/r/cryptoKeys/k", got["kmsKeyName"])
	})

	t.Run("NoKMS", func(t *testing.T) {
		t.Parallel()
		raw := []any{map[string]any{"name": "projects/p/topics/t"}}
		got := extractGCPPubSubConfig(raw)
		assert.NotContains(t, got, "kmsKeyName")
	})

	t.Run("Envelope", func(t *testing.T) {
		t.Parallel()
		raw := map[string]any{"topics": []any{
			map[string]any{"name": "projects/p/topics/wrap"},
		}}
		got := extractGCPPubSubConfig(raw)
		assert.Equal(t, "wrap", got["topicName"])
	})

	t.Run("Empty", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractGCPPubSubConfig(nil))
		assert.Nil(t, extractGCPPubSubConfig([]any{}))
	})
}

// ---------- gcp_cloud_kms ----------

func TestExtractGCPCloudKMSConfig(t *testing.T) {
	t.Parallel()

	t.Run("HappyPath", func(t *testing.T) {
		t.Parallel()
		raw := []any{map[string]any{
			"name":       "projects/demo/locations/global/keyRings/demo-kr",
			"createTime": "2026-03-01T00:00:00Z",
		}}
		got := extractGCPCloudKMSConfig(raw)
		require.NotNil(t, got)
		assert.Equal(t, "1", got["keyringCount"])
		assert.Equal(t, "demo-kr", got["keyringName"])
	})

	t.Run("MultipleRings", func(t *testing.T) {
		t.Parallel()
		raw := []any{
			map[string]any{"name": "projects/p/locations/global/keyRings/a"},
			map[string]any{"name": "projects/p/locations/global/keyRings/b"},
			map[string]any{"name": "projects/p/locations/global/keyRings/c"},
		}
		got := extractGCPCloudKMSConfig(raw)
		assert.Equal(t, "3", got["keyringCount"])
		assert.Equal(t, "a", got["keyringName"])
	})

	t.Run("Envelope", func(t *testing.T) {
		t.Parallel()
		raw := map[string]any{"keyRings": []any{
			map[string]any{"name": "projects/p/locations/global/keyRings/wrap"},
		}}
		got := extractGCPCloudKMSConfig(raw)
		assert.Equal(t, "wrap", got["keyringName"])
	})

	t.Run("Empty", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractGCPCloudKMSConfig(nil))
		assert.Nil(t, extractGCPCloudKMSConfig([]any{}))
	})
}

// ---------- gcp_secret_manager ----------

func TestExtractGCPSecretManagerConfig(t *testing.T) {
	t.Parallel()

	t.Run("HappyPath_Automatic", func(t *testing.T) {
		t.Parallel()
		raw := []any{map[string]any{
			"name":        "projects/demo/secrets/demo-secret",
			"replication": map[string]any{"automatic": map[string]any{}},
		}}
		got := extractGCPSecretManagerConfig(raw)
		require.NotNil(t, got)
		assert.Equal(t, "1", got["secretCount"])
		assert.Equal(t, "demo-secret", got["secretName"])
		assert.Equal(t, "automatic", got["replication"])
	})

	t.Run("UserManaged", func(t *testing.T) {
		t.Parallel()
		raw := []any{map[string]any{
			"name": "projects/p/secrets/x",
			"replication": map[string]any{
				"userManaged": map[string]any{
					"replicas": []any{map[string]any{"location": "us-central1"}},
				},
			},
		}}
		got := extractGCPSecretManagerConfig(raw)
		assert.Equal(t, "user-managed", got["replication"])
	})

	t.Run("ReplicationAbsent", func(t *testing.T) {
		t.Parallel()
		raw := []any{map[string]any{"name": "projects/p/secrets/x"}}
		got := extractGCPSecretManagerConfig(raw)
		assert.NotContains(t, got, "replication")
	})

	t.Run("Envelope", func(t *testing.T) {
		t.Parallel()
		raw := map[string]any{"secrets": []any{
			map[string]any{"name": "projects/p/secrets/wrap"},
		}}
		got := extractGCPSecretManagerConfig(raw)
		assert.Equal(t, "wrap", got["secretName"])
	})

	t.Run("Empty", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractGCPSecretManagerConfig(nil))
		assert.Nil(t, extractGCPSecretManagerConfig([]any{}))
	})
}

// ---------- gcp_cloud_armor ----------

func TestExtractGCPCloudArmorConfig(t *testing.T) {
	t.Parallel()

	t.Run("HappyPath", func(t *testing.T) {
		t.Parallel()
		raw := []any{map[string]any{
			"name":  "demo-policy",
			"type":  "CLOUD_ARMOR",
			"rules": []any{map[string]any{"priority": float64(1000)}, map[string]any{"priority": float64(2000)}},
		}}
		got := extractGCPCloudArmorConfig(raw)
		require.NotNil(t, got)
		assert.Equal(t, "1", got["policyCount"])
		assert.Equal(t, "demo-policy", got["policyName"])
		assert.Equal(t, "CLOUD_ARMOR", got["policyType"])
		assert.Equal(t, "2", got["ruleCount"])
	})

	t.Run("EdgePolicy", func(t *testing.T) {
		t.Parallel()
		raw := []any{map[string]any{
			"name": "edge",
			"type": "CLOUD_ARMOR_EDGE",
		}}
		got := extractGCPCloudArmorConfig(raw)
		assert.Equal(t, "CLOUD_ARMOR_EDGE", got["policyType"])
		assert.NotContains(t, got, "ruleCount")
	})

	t.Run("Envelope", func(t *testing.T) {
		t.Parallel()
		raw := map[string]any{"items": []any{
			map[string]any{"name": "wrap-policy", "type": "CLOUD_ARMOR_NETWORK"},
		}}
		got := extractGCPCloudArmorConfig(raw)
		assert.Equal(t, "wrap-policy", got["policyName"])
		assert.Equal(t, "CLOUD_ARMOR_NETWORK", got["policyType"])
	})

	t.Run("Empty", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractGCPCloudArmorConfig(nil))
		assert.Nil(t, extractGCPCloudArmorConfig([]any{}))
	})
}

// ---------- gcp_identity_platform ----------

func TestExtractGCPIdentityPlatformConfig(t *testing.T) {
	t.Parallel()

	t.Run("HappyPath_MFAEnabled", func(t *testing.T) {
		t.Parallel()
		raw := []any{map[string]any{
			"name":                  "projects/demo/tenants/tn1",
			"displayName":           "Tenant 1",
			"allowPasswordSignup":   true,
			"enableEmailLinkSignin": false,
			"mfaConfig":             map[string]any{"state": "ENABLED"},
		}}
		got := extractGCPIdentityPlatformConfig(raw)
		require.NotNil(t, got)
		assert.Equal(t, "1", got["tenantCount"])
		assert.Equal(t, "tn1", got["tenantName"])
		assert.Equal(t, "Tenant 1", got["displayName"])
		assert.Equal(t, "Yes", got["allowPasswordSignup"])
		assert.Equal(t, "No", got["enableEmailLinkSignin"])
		assert.Equal(t, "Yes", got["mfaRequired"])
	})

	t.Run("MFADisabled", func(t *testing.T) {
		t.Parallel()
		raw := []any{map[string]any{
			"name":      "projects/demo/tenants/tn-off",
			"mfaConfig": map[string]any{"state": "DISABLED"},
		}}
		got := extractGCPIdentityPlatformConfig(raw)
		assert.Equal(t, "No", got["mfaRequired"])
	})

	t.Run("MFAUnspecified", func(t *testing.T) {
		t.Parallel()
		// state="STATE_UNSPECIFIED" → field omitted (switch only matches
		// ENABLED / DISABLED).
		raw := []any{map[string]any{
			"name":      "projects/demo/tenants/tn-u",
			"mfaConfig": map[string]any{"state": "STATE_UNSPECIFIED"},
		}}
		got := extractGCPIdentityPlatformConfig(raw)
		assert.NotContains(t, got, "mfaRequired")
	})

	t.Run("MFAMissing", func(t *testing.T) {
		t.Parallel()
		// No mfaConfig at all → field omitted.
		raw := []any{map[string]any{"name": "projects/demo/tenants/no-mfa"}}
		got := extractGCPIdentityPlatformConfig(raw)
		assert.NotContains(t, got, "mfaRequired")
	})

	t.Run("Envelope", func(t *testing.T) {
		t.Parallel()
		raw := map[string]any{"tenants": []any{
			map[string]any{"name": "projects/p/tenants/wrap", "displayName": "W"},
		}}
		got := extractGCPIdentityPlatformConfig(raw)
		assert.Equal(t, "wrap", got["tenantName"])
		assert.Equal(t, "W", got["displayName"])
	})

	t.Run("Empty", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractGCPIdentityPlatformConfig(nil))
		assert.Nil(t, extractGCPIdentityPlatformConfig([]any{}))
	})
}

// ---------- gcp_vpc ----------

func TestExtractGCPVPCConfig(t *testing.T) {
	t.Parallel()

	t.Run("HappyPath_CustomMode", func(t *testing.T) {
		t.Parallel()
		raw := []any{map[string]any{
			"name":                  "demo-vpc",
			"autoCreateSubnetworks": false,
			"routingConfig":         map[string]any{"routingMode": "REGIONAL"},
			"subnetworks": []any{
				"https://www.googleapis.com/compute/v1/projects/p/regions/us/subnetworks/a",
				"https://www.googleapis.com/compute/v1/projects/p/regions/us/subnetworks/b",
			},
		}}
		got := extractGCPVPCConfig(raw)
		require.NotNil(t, got)
		assert.Equal(t, "1", got["networkCount"])
		assert.Equal(t, "demo-vpc", got["networkName"])
		assert.Equal(t, "No", got["autoCreateSubnetworks"]) // custom mode
		assert.Equal(t, "REGIONAL", got["routingMode"])
		assert.Equal(t, "2", got["subnetworkCount"])
	})

	t.Run("AutoMode", func(t *testing.T) {
		t.Parallel()
		raw := []any{map[string]any{
			"name":                  "auto-vpc",
			"autoCreateSubnetworks": true,
			"routingConfig":         map[string]any{"routingMode": "GLOBAL"},
		}}
		got := extractGCPVPCConfig(raw)
		assert.Equal(t, "Yes", got["autoCreateSubnetworks"]) // auto mode
		assert.Equal(t, "GLOBAL", got["routingMode"])
		// Issue #236 spec calls for a `vpcMode=auto|custom` summary key
		// — the current extractor surfaces only autoCreateSubnetworks
		// directly. Confirm the alias key is NOT present so a future
		// implementer of #236 has a failing assertion to flip.
		assert.NotContains(t, got, "vpcMode")
	})

	t.Run("IGWClassificationNotSurfaced", func(t *testing.T) {
		t.Parallel()
		// The current extractor does NOT inspect firewall rules to
		// classify Public/Private/Mixed (issue #236 calls for it but
		// the inspector returns []computeapi.Network, not firewalls).
		// Lock in current behavior so a future implementer has a
		// failing assertion to flip.
		raw := []any{map[string]any{
			"name":                  "fw-vpc",
			"autoCreateSubnetworks": false,
			"firewalls": []any{
				map[string]any{"sourceRanges": []any{"0.0.0.0/0"}},
				map[string]any{"sourceRanges": []any{"10.0.0.0/8"}},
			},
		}}
		got := extractGCPVPCConfig(raw)
		assert.NotContains(t, got, "igwClassification")
		assert.NotContains(t, got, "internetExposure")
	})

	t.Run("NoSubnetworks", func(t *testing.T) {
		t.Parallel()
		raw := []any{map[string]any{"name": "n", "autoCreateSubnetworks": false}}
		got := extractGCPVPCConfig(raw)
		assert.NotContains(t, got, "subnetworkCount")
	})

	t.Run("Envelope", func(t *testing.T) {
		t.Parallel()
		raw := map[string]any{"items": []any{
			map[string]any{"name": "env-vpc", "autoCreateSubnetworks": true},
		}}
		got := extractGCPVPCConfig(raw)
		assert.Equal(t, "env-vpc", got["networkName"])
		assert.Equal(t, "Yes", got["autoCreateSubnetworks"])
	})

	t.Run("Empty", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractGCPVPCConfig(nil))
		assert.Nil(t, extractGCPVPCConfig([]any{}))
	})
}

// ---------- gcp_loadbalancer ----------

func TestExtractGCPLoadBalancerConfig(t *testing.T) {
	t.Parallel()

	t.Run("HappyPath", func(t *testing.T) {
		t.Parallel()
		raw := []any{map[string]any{
			"name":           "demo-urlmap",
			"defaultService": "https://www.googleapis.com/compute/v1/projects/demo/global/backendServices/demo-backend",
			"hostRules": []any{
				map[string]any{"hosts": []any{"example.com"}},
				map[string]any{"hosts": []any{"www.example.com"}},
			},
		}}
		got := extractGCPLoadBalancerConfig(raw)
		require.NotNil(t, got)
		assert.Equal(t, "1", got["urlMapCount"])
		assert.Equal(t, "demo-urlmap", got["urlMapName"])
		assert.Equal(t, "demo-backend", got["defaultService"]) // basename
		assert.Equal(t, "2", got["hostRuleCount"])
	})

	t.Run("NoHostRules", func(t *testing.T) {
		t.Parallel()
		raw := []any{map[string]any{"name": "no-hr", "defaultService": "x"}}
		got := extractGCPLoadBalancerConfig(raw)
		assert.NotContains(t, got, "hostRuleCount")
	})

	t.Run("Envelope", func(t *testing.T) {
		t.Parallel()
		raw := map[string]any{"items": []any{
			map[string]any{"name": "wrap"},
		}}
		got := extractGCPLoadBalancerConfig(raw)
		assert.Equal(t, "wrap", got["urlMapName"])
	})

	t.Run("Empty", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractGCPLoadBalancerConfig(nil))
		assert.Nil(t, extractGCPLoadBalancerConfig([]any{}))
	})
}

// ---------- gcp_cloud_logging ----------

func TestExtractGCPCloudLoggingConfig(t *testing.T) {
	t.Parallel()

	t.Run("HappyPath", func(t *testing.T) {
		t.Parallel()
		raw := []any{
			"projects/demo/logs/cloudaudit.googleapis.com%2Factivity",
			"projects/demo/logs/run.googleapis.com%2Fstdout",
		}
		got := extractGCPCloudLoggingConfig(raw)
		require.NotNil(t, got)
		assert.Equal(t, "2", got["logCount"])
		assert.Equal(t, "projects/demo/logs/cloudaudit.googleapis.com%2Factivity", got["logName"])
	})

	t.Run("Envelope", func(t *testing.T) {
		t.Parallel()
		raw := map[string]any{"logs": []any{"projects/p/logs/x"}}
		got := extractGCPCloudLoggingConfig(raw)
		assert.Equal(t, "1", got["logCount"])
		assert.Equal(t, "projects/p/logs/x", got["logName"])
	})

	t.Run("EmptyName", func(t *testing.T) {
		t.Parallel()
		raw := []any{""}
		got := extractGCPCloudLoggingConfig(raw)
		assert.Equal(t, "1", got["logCount"])
		assert.NotContains(t, got, "logName")
	})

	t.Run("Empty", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractGCPCloudLoggingConfig(nil))
		assert.Nil(t, extractGCPCloudLoggingConfig([]any{}))
	})
}

// ---------- gcp_cloud_build ----------

func TestExtractGCPCloudBuildConfig(t *testing.T) {
	t.Parallel()

	t.Run("HappyPath", func(t *testing.T) {
		t.Parallel()
		raw := []any{map[string]any{
			"name":     "projects/demo/triggers/deploy-on-main",
			"filename": "cloudbuild.yaml",
			"github":   map[string]any{"owner": "luthersystems", "name": "reliable"},
			"disabled": false,
		}}
		got := extractGCPCloudBuildConfig(raw)
		require.NotNil(t, got)
		assert.Equal(t, "1", got["triggerCount"])
		assert.Equal(t, "deploy-on-main", got["triggerName"]) // basename
		assert.Equal(t, "cloudbuild.yaml", got["filename"])
		assert.Equal(t, "No", got["disabled"])
		assert.Equal(t, "luthersystems/reliable", got["githubRepo"])
	})

	t.Run("DisabledTrigger", func(t *testing.T) {
		t.Parallel()
		raw := []any{map[string]any{
			"name":     "projects/demo/triggers/x",
			"disabled": true,
		}}
		got := extractGCPCloudBuildConfig(raw)
		assert.Equal(t, "Yes", got["disabled"])
	})

	t.Run("OwnerOnlyGithub", func(t *testing.T) {
		t.Parallel()
		raw := []any{map[string]any{
			"name":   "projects/p/triggers/owner-only",
			"github": map[string]any{"owner": "solo"},
		}}
		got := extractGCPCloudBuildConfig(raw)
		assert.Equal(t, "solo", got["githubRepo"])
	})

	t.Run("NoGithub", func(t *testing.T) {
		t.Parallel()
		raw := []any{map[string]any{"name": "projects/p/triggers/local"}}
		got := extractGCPCloudBuildConfig(raw)
		assert.NotContains(t, got, "githubRepo")
	})

	t.Run("Envelope", func(t *testing.T) {
		t.Parallel()
		raw := map[string]any{"triggers": []any{
			map[string]any{"name": "projects/p/triggers/wrap"},
		}}
		got := extractGCPCloudBuildConfig(raw)
		assert.Equal(t, "wrap", got["triggerName"])
	})

	t.Run("Empty", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractGCPCloudBuildConfig(nil))
		assert.Nil(t, extractGCPCloudBuildConfig([]any{}))
	})
}

// ---------- gcp_vertex_ai ----------

func TestExtractGCPVertexAIConfig(t *testing.T) {
	t.Parallel()

	t.Run("HappyPath", func(t *testing.T) {
		t.Parallel()
		raw := []any{map[string]any{
			"name":           "projects/demo/locations/us-central1/endpoints/12345",
			"displayName":    "Demo Endpoint",
			"deployedModels": []any{map[string]any{"id": "m1"}, map[string]any{"id": "m2"}},
		}}
		got := extractGCPVertexAIConfig(raw)
		require.NotNil(t, got)
		assert.Equal(t, "1", got["endpointCount"])
		assert.Equal(t, "12345", got["endpointName"]) // basename
		assert.Equal(t, "us-central1", got["region"])
		assert.Equal(t, "Demo Endpoint", got["displayName"])
		assert.Equal(t, "2", got["deployedModelCount"])
	})

	t.Run("RegionDefaulting_NoLocation", func(t *testing.T) {
		t.Parallel()
		// When the endpoint name has no `/locations/<region>/` segment
		// (truncated SDK output / missing region) the extractor falls
		// back to omitting the region key — there's no static default.
		raw := []any{map[string]any{
			"name":        "endpoints/123",
			"displayName": "no-region",
		}}
		got := extractGCPVertexAIConfig(raw)
		assert.NotContains(t, got, "region")
	})

	t.Run("RegionExplicitNonDefault", func(t *testing.T) {
		t.Parallel()
		raw := []any{map[string]any{
			"name": "projects/p/locations/europe-west4/endpoints/eu1",
		}}
		got := extractGCPVertexAIConfig(raw)
		assert.Equal(t, "europe-west4", got["region"])
	})

	t.Run("NoDeployedModels", func(t *testing.T) {
		t.Parallel()
		// "deployed 0 models" is the silent failure mode the extractor
		// header calls out — count should be 0, not absent.
		raw := []any{map[string]any{
			"name":           "projects/p/locations/us-central1/endpoints/empty",
			"deployedModels": []any{},
		}}
		got := extractGCPVertexAIConfig(raw)
		assert.Equal(t, "0", got["deployedModelCount"])
	})

	t.Run("Envelope", func(t *testing.T) {
		t.Parallel()
		raw := map[string]any{"endpoints": []any{
			map[string]any{"name": "projects/p/locations/us/endpoints/wrap"},
		}}
		got := extractGCPVertexAIConfig(raw)
		assert.Equal(t, "wrap", got["endpointName"])
	})

	t.Run("Empty", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractGCPVertexAIConfig(nil))
		assert.Nil(t, extractGCPVertexAIConfig([]any{}))
	})
}

// ---------- gcp_cloud_monitoring ----------

func TestExtractGCPCloudMonitoringConfig(t *testing.T) {
	t.Parallel()

	t.Run("HappyPath", func(t *testing.T) {
		t.Parallel()
		raw := []any{
			map[string]any{
				"name":        "projects/demo/alertPolicies/abc",
				"displayName": "High Error Rate",
				"enabled":     true,
			},
			map[string]any{
				"name":        "projects/demo/alertPolicies/def",
				"displayName": "Low Memory",
				"enabled":     true,
			},
			map[string]any{
				"name":        "projects/demo/alertPolicies/ghi",
				"displayName": "Disabled Policy",
				"enabled":     false,
			},
		}
		got := extractGCPCloudMonitoringConfig(raw)
		require.NotNil(t, got)
		assert.Equal(t, "3", got["policyCount"])
		assert.Equal(t, "2", got["enabledCount"])
		assert.Equal(t, "abc", got["policyName"])
		assert.Equal(t, "High Error Rate", got["displayName"])
	})

	t.Run("AllDisabled", func(t *testing.T) {
		t.Parallel()
		raw := []any{
			map[string]any{"name": "projects/p/alertPolicies/x", "enabled": false},
			map[string]any{"name": "projects/p/alertPolicies/y", "enabled": false},
		}
		got := extractGCPCloudMonitoringConfig(raw)
		assert.Equal(t, "2", got["policyCount"])
		assert.Equal(t, "0", got["enabledCount"])
	})

	t.Run("Envelope", func(t *testing.T) {
		t.Parallel()
		raw := map[string]any{"alertPolicies": []any{
			map[string]any{"name": "projects/p/alertPolicies/wrap", "enabled": true},
		}}
		got := extractGCPCloudMonitoringConfig(raw)
		assert.Equal(t, "1", got["policyCount"])
		assert.Equal(t, "1", got["enabledCount"])
		assert.Equal(t, "wrap", got["policyName"])
	})

	t.Run("Empty", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractGCPCloudMonitoringConfig(nil))
		assert.Nil(t, extractGCPCloudMonitoringConfig([]any{}))
	})
}

// ---------- gcp_cloud_functions ----------

func TestExtractGCPCloudFunctionsConfig(t *testing.T) {
	t.Parallel()

	t.Run("HappyPath", func(t *testing.T) {
		t.Parallel()
		raw := []any{map[string]any{
			"name":        "projects/demo/locations/us-central1/functions/demo-fn",
			"state":       "ACTIVE",
			"buildConfig": map[string]any{"runtime": "go122"},
			"serviceConfig": map[string]any{
				"availableMemory": "256M",
				"timeoutSeconds":  float64(60),
			},
		}}
		got := extractGCPCloudFunctionsConfig(raw)
		require.NotNil(t, got)
		assert.Equal(t, "1", got["functionCount"])
		assert.Equal(t, "demo-fn", got["functionName"]) // basename
		assert.Equal(t, "ACTIVE", got["state"])
		assert.Equal(t, "go122", got["runtime"])
	})

	t.Run("NoBuildConfig", func(t *testing.T) {
		t.Parallel()
		raw := []any{map[string]any{
			"name":  "projects/p/locations/us/functions/x",
			"state": "ACTIVE",
		}}
		got := extractGCPCloudFunctionsConfig(raw)
		assert.NotContains(t, got, "runtime")
	})

	t.Run("Envelope", func(t *testing.T) {
		t.Parallel()
		raw := map[string]any{"functions": []any{
			map[string]any{"name": "projects/p/locations/us/functions/wrap"},
		}}
		got := extractGCPCloudFunctionsConfig(raw)
		assert.Equal(t, "wrap", got["functionName"])
	})

	t.Run("Empty", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractGCPCloudFunctionsConfig(nil))
		assert.Nil(t, extractGCPCloudFunctionsConfig([]any{}))
	})
}

// ---------- gcp_api_gateway ----------

func TestExtractGCPAPIGatewayConfig(t *testing.T) {
	t.Parallel()

	t.Run("HappyPath", func(t *testing.T) {
		t.Parallel()
		raw := []any{map[string]any{
			"name":        "projects/demo/locations/global/apis/demo-api",
			"displayName": "Demo API",
			"state":       "ACTIVE",
		}}
		got := extractGCPAPIGatewayConfig(raw)
		require.NotNil(t, got)
		assert.Equal(t, "1", got["apiCount"])
		assert.Equal(t, "demo-api", got["apiName"])
		assert.Equal(t, "Demo API", got["displayName"])
		assert.Equal(t, "ACTIVE", got["state"])
	})

	t.Run("FailedState", func(t *testing.T) {
		t.Parallel()
		raw := []any{map[string]any{
			"name":  "projects/p/locations/global/apis/x",
			"state": "FAILED",
		}}
		got := extractGCPAPIGatewayConfig(raw)
		assert.Equal(t, "FAILED", got["state"])
	})

	t.Run("Envelope", func(t *testing.T) {
		t.Parallel()
		raw := map[string]any{"apis": []any{
			map[string]any{"name": "projects/p/locations/global/apis/wrap"},
		}}
		got := extractGCPAPIGatewayConfig(raw)
		assert.Equal(t, "wrap", got["apiName"])
	})

	t.Run("Empty", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractGCPAPIGatewayConfig(nil))
		assert.Nil(t, extractGCPAPIGatewayConfig([]any{}))
	})
}

// ---------- gcp_cloud_cdn ----------

func TestExtractGCPCloudCDNConfig(t *testing.T) {
	t.Parallel()

	t.Run("HappyPath", func(t *testing.T) {
		t.Parallel()
		raw := []any{map[string]any{
			"name":      "demo-backend",
			"enableCDN": true,
			"cdnPolicy": map[string]any{"cacheMode": "CACHE_ALL_STATIC"},
		}}
		got := extractGCPCloudCDNConfig(raw)
		require.NotNil(t, got)
		assert.Equal(t, "1", got["backendCount"])
		assert.Equal(t, "demo-backend", got["backendName"])
		assert.Equal(t, "Yes", got["enableCdn"])
		assert.Equal(t, "CACHE_ALL_STATIC", got["cacheMode"])
	})

	t.Run("UseOriginHeaders", func(t *testing.T) {
		t.Parallel()
		raw := []any{map[string]any{
			"name":      "use-origin",
			"enableCDN": true,
			"cdnPolicy": map[string]any{"cacheMode": "USE_ORIGIN_HEADERS"},
		}}
		got := extractGCPCloudCDNConfig(raw)
		assert.Equal(t, "USE_ORIGIN_HEADERS", got["cacheMode"])
	})

	t.Run("CDNDisabled", func(t *testing.T) {
		t.Parallel()
		raw := []any{map[string]any{
			"name":      "off-backend",
			"enableCDN": false,
		}}
		got := extractGCPCloudCDNConfig(raw)
		assert.Equal(t, "No", got["enableCdn"])
		assert.NotContains(t, got, "cacheMode")
	})

	t.Run("Envelope", func(t *testing.T) {
		t.Parallel()
		raw := map[string]any{"backendServices": []any{
			map[string]any{"name": "wrap-backend", "enableCDN": true},
		}}
		got := extractGCPCloudCDNConfig(raw)
		assert.Equal(t, "wrap-backend", got["backendName"])
	})

	t.Run("Empty", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractGCPCloudCDNConfig(nil))
		assert.Nil(t, extractGCPCloudCDNConfig([]any{}))
	})
}

// ---------- gcp_bastion ----------

func TestExtractGCPBastionConfig(t *testing.T) {
	t.Parallel()

	t.Run("HappyPath", func(t *testing.T) {
		t.Parallel()
		raw := []any{map[string]any{
			"name":        "demo-bastion",
			"machineType": "https://www.googleapis.com/compute/v1/projects/demo/zones/us-central1-a/machineTypes/e2-small",
			"zone":        "https://www.googleapis.com/compute/v1/projects/demo/zones/us-central1-a",
			"status":      "RUNNING",
		}}
		got := extractGCPBastionConfig(raw)
		require.NotNil(t, got)
		assert.Equal(t, "1", got["instanceCount"])
		assert.Equal(t, "demo-bastion", got["instanceName"])
		assert.Equal(t, "e2-small", got["machineType"])
		assert.Equal(t, "us-central1-a", got["zone"])
		assert.Equal(t, "RUNNING", got["status"])
	})

	t.Run("StoppedBastion", func(t *testing.T) {
		t.Parallel()
		raw := []any{map[string]any{
			"name":   "stopped-bastion",
			"status": "TERMINATED",
		}}
		got := extractGCPBastionConfig(raw)
		assert.Equal(t, "TERMINATED", got["status"])
	})

	t.Run("Envelope", func(t *testing.T) {
		t.Parallel()
		raw := map[string]any{"instances": []any{
			map[string]any{"name": "wrap-bastion"},
		}}
		got := extractGCPBastionConfig(raw)
		assert.Equal(t, "wrap-bastion", got["instanceName"])
	})

	t.Run("Empty", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractGCPBastionConfig(nil))
		assert.Nil(t, extractGCPBastionConfig([]any{}))
	})
}

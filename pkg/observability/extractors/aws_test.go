// Per-extractor field-coverage tests for the AWS extractors in
// extractors.go. Closes Gap 1 of issue #236 — the existing
// extractors_drift_test.go only proves dispatch wiring + happy-path
// non-nil outputs; this file enumerates every field branch in every
// AWS extractor with table-driven cases.
//
// Style notes:
//   - Fixtures are inline `map[string]any` (not JSON strings) so the
//     test reader sees the exact envelope shape the extractor parses.
//   - All tests use t.Parallel().
//   - Every extractor has at minimum: HappyPath, Empty (nil + empty
//     envelope), and one envelope-shape variant where the extractor
//     accepts both flat-slice and {Key: [...]} forms.
//   - Branch cases (multiAZ Yes/No, EC2 stopped instances not counted,
//     OpenSearch managed-vs-AOSS priority, Bedrock IAMRole fallback,
//     ElastiCache primary-only omitting replicas, etc.) are exercised
//     individually with bespoke fixtures.
package extractors

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- extractRDSConfig ---

func TestExtractRDSConfig(t *testing.T) {
	t.Parallel()

	t.Run("HappyPath", func(t *testing.T) {
		t.Parallel()
		got := extractRDSConfig(map[string]any{
			"DBInstances": []any{
				map[string]any{
					"DBInstanceClass":  "db.t3.medium",
					"AllocatedStorage": float64(100),
					"Engine":           "postgres",
					"MultiAZ":          true,
				},
			},
		})
		require.NotNil(t, got)
		assert.Equal(t, "db.t3.medium", got["cpuSize"])
		assert.Equal(t, "100 GB", got["storageSize"])
		assert.Equal(t, "postgres", got["engine"])
		assert.Equal(t, "Yes", got["multiAz"])
	})

	t.Run("MultiAZFalse", func(t *testing.T) {
		t.Parallel()
		got := extractRDSConfig(map[string]any{
			"DBInstances": []any{
				map[string]any{"DBInstanceClass": "db.t3.micro", "MultiAZ": false},
			},
		})
		require.NotNil(t, got)
		assert.Equal(t, "No", got["multiAz"])
	})

	t.Run("MultiAZTrue", func(t *testing.T) {
		t.Parallel()
		got := extractRDSConfig(map[string]any{
			"DBInstances": []any{
				map[string]any{"DBInstanceClass": "db.t3.micro", "MultiAZ": true},
			},
		})
		require.NotNil(t, got)
		assert.Equal(t, "Yes", got["multiAz"])
	})

	t.Run("NilInput", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractRDSConfig(nil))
	})

	t.Run("EmptyEnvelope", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractRDSConfig(map[string]any{"DBInstances": []any{}}))
	})

	t.Run("PartialFields", func(t *testing.T) {
		t.Parallel()
		got := extractRDSConfig(map[string]any{
			"DBInstances": []any{
				map[string]any{"Engine": "mysql"},
			},
		})
		require.NotNil(t, got)
		assert.Equal(t, "mysql", got["engine"])
		_, hasClass := got["cpuSize"]
		_, hasStorage := got["storageSize"]
		assert.False(t, hasClass)
		assert.False(t, hasStorage)
	})
}

// --- extractEC2Config ---

func TestExtractEC2Config(t *testing.T) {
	t.Parallel()

	t.Run("HappyPath", func(t *testing.T) {
		t.Parallel()
		got := extractEC2Config(map[string]any{
			"Reservations": []any{
				map[string]any{"Instances": []any{
					map[string]any{
						"InstanceType": "t3.small",
						"State":        map[string]any{"Name": "running"},
					},
					map[string]any{
						"InstanceType": "t3.small",
						"State":        map[string]any{"Name": "running"},
					},
				}},
			},
		})
		require.NotNil(t, got)
		assert.Equal(t, "t3.small", got["instanceType"])
		assert.Equal(t, "2", got["numServers"])
	})

	t.Run("PartialFields", func(t *testing.T) {
		t.Parallel()
		// Only InstanceType, no State — instanceType set, no numServers.
		got := extractEC2Config(map[string]any{
			"Reservations": []any{
				map[string]any{"Instances": []any{
					map[string]any{"InstanceType": "t3.large"},
				}},
			},
		})
		require.NotNil(t, got)
		assert.Equal(t, "t3.large", got["instanceType"])
		_, hasCount := got["numServers"]
		assert.False(t, hasCount)
	})

	t.Run("StoppedInstancesNotCounted", func(t *testing.T) {
		t.Parallel()
		got := extractEC2Config(map[string]any{
			"Reservations": []any{
				map[string]any{"Instances": []any{
					map[string]any{
						"InstanceType": "t3.small",
						"State":        map[string]any{"Name": "stopped"},
					},
				}},
			},
		})
		require.NotNil(t, got)
		assert.Equal(t, "t3.small", got["instanceType"])
		_, hasCount := got["numServers"]
		assert.False(t, hasCount, "stopped instance should not increment numServers")
	})

	t.Run("MixedRunningAndStopped", func(t *testing.T) {
		t.Parallel()
		got := extractEC2Config(map[string]any{
			"Reservations": []any{
				map[string]any{"Instances": []any{
					map[string]any{
						"InstanceType": "t3.small",
						"State":        map[string]any{"Name": "running"},
					},
					map[string]any{
						"InstanceType": "t3.small",
						"State":        map[string]any{"Name": "stopped"},
					},
					map[string]any{
						"InstanceType": "t3.small",
						"State":        map[string]any{"Name": "running"},
					},
				}},
			},
		})
		require.NotNil(t, got)
		assert.Equal(t, "2", got["numServers"])
	})

	t.Run("NilInput", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractEC2Config(nil))
	})

	t.Run("EmptyEnvelope", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractEC2Config(map[string]any{"Reservations": []any{}}))
	})
}

// --- extractElastiCacheConfig ---

func TestExtractElastiCacheConfig(t *testing.T) {
	t.Parallel()

	t.Run("HappyPath", func(t *testing.T) {
		t.Parallel()
		got := extractElastiCacheConfig(map[string]any{
			"CacheClusters": []any{
				map[string]any{
					"CacheNodeType": "cache.t3.micro",
					"Engine":        "redis",
					"NumCacheNodes": float64(3),
				},
			},
		})
		require.NotNil(t, got)
		assert.Equal(t, "cache.t3.micro", got["nodeSize"])
		assert.Equal(t, "redis", got["engine"])
		// 3 nodes - 1 primary = 2 replicas
		assert.Equal(t, "2", got["replicas"])
	})

	t.Run("PrimaryOnlyOmitsReplicas", func(t *testing.T) {
		t.Parallel()
		got := extractElastiCacheConfig(map[string]any{
			"CacheClusters": []any{
				map[string]any{
					"CacheNodeType": "cache.t3.micro",
					"NumCacheNodes": float64(1),
				},
			},
		})
		require.NotNil(t, got)
		assert.Equal(t, "cache.t3.micro", got["nodeSize"])
		_, hasReplicas := got["replicas"]
		assert.False(t, hasReplicas, "NumCacheNodes=1 should omit replicas key")
	})

	t.Run("ZeroNodesOmitsReplicas", func(t *testing.T) {
		t.Parallel()
		got := extractElastiCacheConfig(map[string]any{
			"CacheClusters": []any{
				map[string]any{
					"CacheNodeType": "cache.t3.micro",
					"NumCacheNodes": float64(0),
				},
			},
		})
		require.NotNil(t, got)
		_, hasReplicas := got["replicas"]
		assert.False(t, hasReplicas)
	})

	t.Run("NilInput", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractElastiCacheConfig(nil))
	})

	t.Run("EmptyEnvelope", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractElastiCacheConfig(map[string]any{"CacheClusters": []any{}}))
	})
}

// --- extractOpenSearchConfig ---

func TestExtractOpenSearchConfig(t *testing.T) {
	t.Parallel()

	t.Run("ManagedHappyPath", func(t *testing.T) {
		t.Parallel()
		got := extractOpenSearchConfig(map[string]any{
			"DomainStatusList": []any{
				map[string]any{
					"DomainName": "demo",
					"ClusterConfig": map[string]any{
						"InstanceType":         "r6g.large.search",
						"InstanceCount":        float64(3),
						"ZoneAwarenessEnabled": true,
					},
					"EBSOptions": map[string]any{
						"VolumeSize": float64(50),
					},
				},
			},
		})
		require.NotNil(t, got)
		assert.Equal(t, "managed", got["deploymentType"])
		assert.Equal(t, "r6g.large.search", got["instanceType"])
		assert.Equal(t, "3", got["instanceCount"])
		assert.Equal(t, "Yes", got["multiAz"])
		assert.Equal(t, "50 GB", got["storageSize"])
	})

	t.Run("ManagedFlatSlice", func(t *testing.T) {
		t.Parallel()
		got := extractOpenSearchConfig([]any{
			map[string]any{
				"DomainName":    "demo",
				"ClusterConfig": map[string]any{"InstanceType": "r6g.large.search"},
			},
		})
		require.NotNil(t, got)
		assert.Equal(t, "managed", got["deploymentType"])
		assert.Equal(t, "r6g.large.search", got["instanceType"])
	})

	t.Run("ManagedWinsOverAOSS", func(t *testing.T) {
		t.Parallel()
		// AOSS first then managed — managed should still win.
		got := extractOpenSearchConfig([]any{
			map[string]any{"Name": "vector-coll", "Id": "xyz", "Type": "VECTORSEARCH"},
			map[string]any{
				"DomainName":    "demo",
				"ClusterConfig": map[string]any{"InstanceType": "m6g.large.search"},
			},
		})
		require.NotNil(t, got)
		assert.Equal(t, "managed", got["deploymentType"])
		assert.Equal(t, "m6g.large.search", got["instanceType"])
	})

	t.Run("AOSSCollection", func(t *testing.T) {
		t.Parallel()
		got := extractOpenSearchConfig([]any{
			map[string]any{
				"Name":   "demo-vec",
				"Id":     "aoss-1",
				"Status": "ACTIVE",
				"Type":   "VECTORSEARCH",
			},
		})
		require.NotNil(t, got)
		assert.Equal(t, "serverless", got["deploymentType"])
		assert.Equal(t, "demo-vec", got["collectionName"])
		assert.Equal(t, "ACTIVE", got["status"])
		assert.Equal(t, "VECTORSEARCH", got["collectionType"])
	})

	t.Run("AOSSWithoutType", func(t *testing.T) {
		t.Parallel()
		got := extractOpenSearchConfig([]any{
			map[string]any{"Name": "demo-coll", "Id": "aoss-2"},
		})
		require.NotNil(t, got)
		assert.Equal(t, "serverless", got["deploymentType"])
		assert.Equal(t, "demo-coll", got["collectionName"])
		_, hasType := got["collectionType"]
		assert.False(t, hasType)
	})

	t.Run("ManagedDeploymentTypeOnlyReturnsNil", func(t *testing.T) {
		t.Parallel()
		// ClusterConfig present but empty, no EBSOptions — only deploymentType
		// would be set, which the extractor returns as nil.
		got := extractOpenSearchConfig([]any{
			map[string]any{"ClusterConfig": map[string]any{}},
		})
		assert.Nil(t, got)
	})

	t.Run("NoneMatch", func(t *testing.T) {
		t.Parallel()
		got := extractOpenSearchConfig([]any{
			map[string]any{"SomeUnknownField": "x"},
		})
		assert.Nil(t, got)
	})

	t.Run("NilInput", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractOpenSearchConfig(nil))
	})

	t.Run("EmptyEnvelope", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractOpenSearchConfig(map[string]any{"DomainStatusList": []any{}}))
	})
}

// --- extractLambdaConfig ---

func TestExtractLambdaConfig(t *testing.T) {
	t.Parallel()

	t.Run("HappyPath", func(t *testing.T) {
		t.Parallel()
		got := extractLambdaConfig(map[string]any{
			"Functions": []any{
				map[string]any{
					"Runtime":    "go1.x",
					"MemorySize": float64(256),
					"Timeout":    float64(30),
				},
			},
		})
		require.NotNil(t, got)
		assert.Equal(t, "go1.x", got["runtime"])
		assert.Equal(t, "256", got["memorySize"])
		assert.Equal(t, "30s", got["timeout"])
	})

	t.Run("PartialMemoryOnly", func(t *testing.T) {
		t.Parallel()
		got := extractLambdaConfig(map[string]any{
			"Functions": []any{
				map[string]any{"MemorySize": float64(128)},
			},
		})
		require.NotNil(t, got)
		assert.Equal(t, "128", got["memorySize"])
		_, hasRuntime := got["runtime"]
		_, hasTimeout := got["timeout"]
		assert.False(t, hasRuntime)
		assert.False(t, hasTimeout)
	})

	t.Run("NilInput", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractLambdaConfig(nil))
	})

	t.Run("EmptyEnvelope", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractLambdaConfig(map[string]any{"Functions": []any{}}))
	})
}

// --- extractMSKConfig ---

func TestExtractMSKConfig(t *testing.T) {
	t.Parallel()

	t.Run("BrokerCountAndType", func(t *testing.T) {
		t.Parallel()
		got := extractMSKConfig(map[string]any{
			"ClusterInfoList": []any{
				map[string]any{
					"BrokerNodeGroupInfo": map[string]any{"InstanceType": "kafka.m5.large"},
					"NumberOfBrokerNodes": float64(3),
				},
			},
		})
		require.NotNil(t, got)
		assert.Equal(t, "kafka.m5.large", got["brokerInstanceType"])
		assert.Equal(t, "3", got["brokerCount"])
	})

	t.Run("CountOnly", func(t *testing.T) {
		t.Parallel()
		got := extractMSKConfig(map[string]any{
			"ClusterInfoList": []any{
				map[string]any{"NumberOfBrokerNodes": float64(2)},
			},
		})
		require.NotNil(t, got)
		assert.Equal(t, "2", got["brokerCount"])
		_, hasInst := got["brokerInstanceType"]
		assert.False(t, hasInst)
	})

	t.Run("NilInput", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractMSKConfig(nil))
	})

	t.Run("EmptyEnvelope", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractMSKConfig(map[string]any{"ClusterInfoList": []any{}}))
	})
}

// --- extractALBConfig ---

func TestExtractALBConfig(t *testing.T) {
	t.Parallel()

	t.Run("HappyPath_FlatSlice", func(t *testing.T) {
		t.Parallel()
		got := extractALBConfig([]any{
			map[string]any{
				"LoadBalancerName": "demo-alb",
				"Type":             "application",
				"Scheme":           "internet-facing",
				"DNSName":          "demo-alb-123.us-east-1.elb.amazonaws.com",
				"State":            map[string]any{"Code": "active"},
			},
			map[string]any{"LoadBalancerName": "second-alb", "Type": "network"},
		})
		require.NotNil(t, got)
		assert.Equal(t, "demo-alb", got["loadBalancerName"])
		assert.Equal(t, "application", got["loadBalancerType"])
		assert.Equal(t, "internet-facing", got["scheme"])
		assert.Equal(t, "demo-alb-123.us-east-1.elb.amazonaws.com", got["dnsName"])
		assert.Equal(t, "active", got["state"])
		assert.Equal(t, "2", got["count"])
	})

	t.Run("HappyPath_Envelope", func(t *testing.T) {
		t.Parallel()
		got := extractALBConfig(map[string]any{
			"LoadBalancers": []any{
				map[string]any{"LoadBalancerName": "envelope-alb", "Type": "application"},
			},
		})
		require.NotNil(t, got)
		assert.Equal(t, "envelope-alb", got["loadBalancerName"])
		assert.Equal(t, "1", got["count"])
	})

	t.Run("CountOnlyReturnsNil", func(t *testing.T) {
		t.Parallel()
		// Item present but has no useful fields — extractor returns nil
		// when the only key emitted is `count`.
		got := extractALBConfig([]any{
			map[string]any{"SomeUnrelated": "x"},
		})
		assert.Nil(t, got)
	})

	t.Run("NilInput", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractALBConfig(nil))
	})

	t.Run("EmptyEnvelope", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractALBConfig(map[string]any{"LoadBalancers": []any{}}))
	})
}

// --- extractKMSConfig ---

func TestExtractKMSConfig(t *testing.T) {
	t.Parallel()

	t.Run("HappyPath_FlatSlice", func(t *testing.T) {
		t.Parallel()
		got := extractKMSConfig([]any{
			map[string]any{"AliasName": "alias/demo-1", "TargetKeyId": "kid-1"},
			map[string]any{"AliasName": "alias/demo-2", "TargetKeyId": "kid-2"},
			map[string]any{"AliasName": "alias/aws/s3"}, // no TargetKeyId — filtered
		})
		require.NotNil(t, got)
		assert.Equal(t, "2", got["numKeys"])
	})

	t.Run("HappyPath_Envelope", func(t *testing.T) {
		t.Parallel()
		got := extractKMSConfig(map[string]any{
			"Aliases": []any{
				map[string]any{"AliasName": "alias/demo", "TargetKeyId": "abc"},
			},
		})
		require.NotNil(t, got)
		assert.Equal(t, "1", got["numKeys"])
	})

	t.Run("FallbackToTotalCount", func(t *testing.T) {
		t.Parallel()
		// All entries lack TargetKeyId — fall back to len(items).
		got := extractKMSConfig([]any{
			map[string]any{"AliasName": "alias/aws/s3"},
			map[string]any{"AliasName": "alias/aws/lambda"},
		})
		require.NotNil(t, got)
		assert.Equal(t, "2", got["numKeys"])
	})

	t.Run("NilInput", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractKMSConfig(nil))
	})

	t.Run("EmptyEnvelope", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractKMSConfig(map[string]any{"Aliases": []any{}}))
	})
}

// --- extractS3Config ---

func TestExtractS3Config(t *testing.T) {
	t.Parallel()

	t.Run("HappyPath_FlatSlice", func(t *testing.T) {
		t.Parallel()
		got := extractS3Config([]any{
			map[string]any{"Name": "demo-bucket-1"},
			map[string]any{"Name": "demo-bucket-2"},
			map[string]any{"Name": "demo-bucket-3"},
		})
		require.NotNil(t, got)
		assert.Equal(t, "3", got["bucketCount"])
	})

	t.Run("HappyPath_Envelope", func(t *testing.T) {
		t.Parallel()
		got := extractS3Config(map[string]any{
			"Buckets": []any{map[string]any{"Name": "demo-bucket"}},
		})
		require.NotNil(t, got)
		assert.Equal(t, "1", got["bucketCount"])
	})

	t.Run("NilInput", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractS3Config(nil))
	})

	t.Run("EmptyEnvelope", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractS3Config(map[string]any{"Buckets": []any{}}))
	})
}

// --- extractSecretsManagerConfig ---

func TestExtractSecretsManagerConfig(t *testing.T) {
	t.Parallel()

	t.Run("HappyPath_FlatSlice", func(t *testing.T) {
		t.Parallel()
		got := extractSecretsManagerConfig([]any{
			map[string]any{"Name": "demo/secret-1"},
			map[string]any{"Name": "demo/secret-2"},
		})
		require.NotNil(t, got)
		assert.Equal(t, "2", got["numSecrets"])
	})

	t.Run("HappyPath_Envelope", func(t *testing.T) {
		t.Parallel()
		got := extractSecretsManagerConfig(map[string]any{
			"SecretList": []any{
				map[string]any{"Name": "demo/secret"},
			},
		})
		require.NotNil(t, got)
		assert.Equal(t, "1", got["numSecrets"])
	})

	t.Run("NilInput", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractSecretsManagerConfig(nil))
	})

	t.Run("EmptyEnvelope", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractSecretsManagerConfig(map[string]any{"SecretList": []any{}}))
	})
}

// --- extractVPCConfig ---

func TestExtractVPCConfig(t *testing.T) {
	t.Parallel()

	t.Run("PublicVPC", func(t *testing.T) {
		t.Parallel()
		got := extractVPCConfig([]any{
			map[string]any{
				"VpcId":              "vpc-1",
				"CidrBlock":          "10.0.0.0/16",
				"State":              "available",
				"HasInternetGateway": true,
			},
		})
		require.NotNil(t, got)
		assert.Equal(t, "vpc-1", got["vpcId"])
		assert.Equal(t, "10.0.0.0/16", got["cidrBlock"])
		assert.Equal(t, "available", got["state"])
		assert.Equal(t, "public", got["deploymentType"])
	})

	t.Run("PrivateVPC", func(t *testing.T) {
		t.Parallel()
		got := extractVPCConfig([]any{
			map[string]any{
				"VpcId":              "vpc-1",
				"CidrBlock":          "10.0.0.0/16",
				"HasInternetGateway": false,
			},
		})
		require.NotNil(t, got)
		assert.Equal(t, "private", got["deploymentType"])
	})

	t.Run("MixedVPCs", func(t *testing.T) {
		t.Parallel()
		got := extractVPCConfig([]any{
			map[string]any{"VpcId": "vpc-pub", "HasInternetGateway": true},
			map[string]any{"VpcId": "vpc-priv", "HasInternetGateway": false},
		})
		require.NotNil(t, got)
		assert.Equal(t, "mixed", got["deploymentType"])
	})

	t.Run("NoIGWFlagOmitsDeploymentType", func(t *testing.T) {
		t.Parallel()
		got := extractVPCConfig([]any{
			map[string]any{"VpcId": "vpc-1", "CidrBlock": "10.0.0.0/16"},
		})
		require.NotNil(t, got)
		assert.Equal(t, "vpc-1", got["vpcId"])
		_, has := got["deploymentType"]
		assert.False(t, has, "deploymentType only emitted when HasInternetGateway is present")
	})

	t.Run("HappyPath_Envelope", func(t *testing.T) {
		t.Parallel()
		got := extractVPCConfig(map[string]any{
			"Vpcs": []any{
				map[string]any{"VpcId": "vpc-env", "CidrBlock": "172.16.0.0/16"},
			},
		})
		require.NotNil(t, got)
		assert.Equal(t, "vpc-env", got["vpcId"])
	})

	t.Run("NilInput", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractVPCConfig(nil))
	})

	t.Run("EmptyEnvelope", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractVPCConfig(map[string]any{"Vpcs": []any{}}))
	})
}

// --- extractAPIGatewayConfig ---

func TestExtractAPIGatewayConfig(t *testing.T) {
	t.Parallel()

	t.Run("HTTPApi", func(t *testing.T) {
		t.Parallel()
		got := extractAPIGatewayConfig(map[string]any{
			"Items": []any{
				map[string]any{
					"ApiId":        "abc123",
					"Name":         "demo-http-api",
					"ProtocolType": "HTTP",
					"ApiEndpoint":  "https://abc123.execute-api.us-east-1.amazonaws.com",
				},
			},
		})
		require.NotNil(t, got)
		assert.Equal(t, "1", got["apiCount"])
		assert.Equal(t, "HTTP", got["protocolType"])
		assert.Equal(t, "https://abc123.execute-api.us-east-1.amazonaws.com", got["domainName"])
	})

	t.Run("WebSocketApi", func(t *testing.T) {
		t.Parallel()
		got := extractAPIGatewayConfig(map[string]any{
			"Items": []any{
				map[string]any{"ApiId": "ws1", "ProtocolType": "WEBSOCKET"},
				map[string]any{"ApiId": "ws2", "ProtocolType": "WEBSOCKET"},
			},
		})
		require.NotNil(t, got)
		assert.Equal(t, "2", got["apiCount"])
		assert.Equal(t, "WEBSOCKET", got["protocolType"])
	})

	t.Run("EndpointTypeForwardCompat", func(t *testing.T) {
		t.Parallel()
		got := extractAPIGatewayConfig(map[string]any{
			"Items": []any{
				map[string]any{"ApiId": "rest1", "EndpointType": "REGIONAL"},
			},
		})
		require.NotNil(t, got)
		assert.Equal(t, "REGIONAL", got["endpointType"])
	})

	t.Run("FlatSlice", func(t *testing.T) {
		t.Parallel()
		got := extractAPIGatewayConfig([]any{
			map[string]any{"ApiId": "abc", "ProtocolType": "HTTP"},
		})
		require.NotNil(t, got)
		assert.Equal(t, "1", got["apiCount"])
	})

	t.Run("NilInput", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractAPIGatewayConfig(nil))
	})

	t.Run("EmptyEnvelope", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractAPIGatewayConfig(map[string]any{"Items": []any{}}))
	})
}

// --- extractBedrockConfig ---

func TestExtractBedrockConfig(t *testing.T) {
	t.Parallel()

	t.Run("KnowledgeBaseHappyPath", func(t *testing.T) {
		t.Parallel()
		got := extractBedrockConfig([]any{
			map[string]any{
				"Name":            "demo-kb",
				"Status":          "ACTIVE",
				"KnowledgeBaseId": "kb-1",
			},
		})
		require.NotNil(t, got)
		assert.Equal(t, "demo-kb", got["knowledgeBaseName"])
		assert.Equal(t, "ACTIVE", got["status"])
		assert.Equal(t, "kb-1", got["knowledgeBaseId"])
	})

	t.Run("IAMRoleFallback", func(t *testing.T) {
		t.Parallel()
		got := extractBedrockConfig([]any{
			map[string]any{
				"Kind":     "IAMRole",
				"RoleName": "demo-bedrock-role",
				"Arn":      "arn:aws:iam::123:role/demo-bedrock-role",
			},
		})
		require.NotNil(t, got)
		assert.Equal(t, "iam-role-only", got["deploymentStage"])
		assert.Equal(t, "demo-bedrock-role", got["roleName"])
		assert.Equal(t, "arn:aws:iam::123:role/demo-bedrock-role", got["roleArn"])
	})

	t.Run("KBWinsOverIAMRole", func(t *testing.T) {
		t.Parallel()
		// Mixed list: KB takes priority even when IAMRole entry comes first.
		got := extractBedrockConfig([]any{
			map[string]any{"Kind": "IAMRole", "RoleName": "demo-role"},
			map[string]any{"Name": "real-kb", "KnowledgeBaseId": "kb-1"},
		})
		require.NotNil(t, got)
		assert.Equal(t, "real-kb", got["knowledgeBaseName"])
		_, hasStage := got["deploymentStage"]
		assert.False(t, hasStage)
	})

	t.Run("HappyPath_Envelope", func(t *testing.T) {
		t.Parallel()
		got := extractBedrockConfig(map[string]any{
			"KnowledgeBaseSummaries": []any{
				map[string]any{"Name": "envelope-kb", "KnowledgeBaseId": "kb-env"},
			},
		})
		require.NotNil(t, got)
		assert.Equal(t, "envelope-kb", got["knowledgeBaseName"])
	})

	t.Run("NoMatch", func(t *testing.T) {
		t.Parallel()
		// Entry with neither Kind=IAMRole nor any KB identifying field.
		got := extractBedrockConfig([]any{
			map[string]any{"Foo": "bar"},
		})
		assert.Nil(t, got)
	})

	t.Run("NilInput", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractBedrockConfig(nil))
	})

	t.Run("EmptyEnvelope", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractBedrockConfig(map[string]any{"KnowledgeBaseSummaries": []any{}}))
	})
}

// --- extractCloudFrontConfig ---

func TestExtractCloudFrontConfig(t *testing.T) {
	t.Parallel()

	t.Run("DistributionList_FlatSlice", func(t *testing.T) {
		t.Parallel()
		got := extractCloudFrontConfig([]any{
			map[string]any{
				"Id":         "E123ABC",
				"DomainName": "d1.cloudfront.net",
				"Status":     "Deployed",
				"Enabled":    true,
			},
		})
		require.NotNil(t, got)
		assert.Equal(t, "E123ABC", got["distributionId"])
		assert.Equal(t, "d1.cloudfront.net", got["domainName"])
		assert.Equal(t, "Deployed", got["status"])
		assert.Equal(t, "Yes", got["enabled"])
	})

	t.Run("ItemsEnvelope", func(t *testing.T) {
		t.Parallel()
		got := extractCloudFrontConfig(map[string]any{
			"Items": []any{
				map[string]any{"Id": "E456", "DomainName": "d2.cloudfront.net"},
			},
		})
		require.NotNil(t, got)
		assert.Equal(t, "E456", got["distributionId"])
		assert.Equal(t, "d2.cloudfront.net", got["domainName"])
	})

	t.Run("DisabledDistribution", func(t *testing.T) {
		t.Parallel()
		got := extractCloudFrontConfig([]any{
			map[string]any{"Id": "E789", "Enabled": false},
		})
		require.NotNil(t, got)
		assert.Equal(t, "No", got["enabled"])
	})

	t.Run("NilInput", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractCloudFrontConfig(nil))
	})

	t.Run("EmptyEnvelope", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractCloudFrontConfig(map[string]any{"Items": []any{}}))
	})
}

// --- extractSQSConfig ---

func TestExtractSQSConfig(t *testing.T) {
	t.Parallel()

	t.Run("StandardQueues_FlatSlice", func(t *testing.T) {
		t.Parallel()
		got := extractSQSConfig([]any{
			"https://sqs.us-east-1.amazonaws.com/123/demo-1",
			"https://sqs.us-east-1.amazonaws.com/123/demo-2",
		})
		require.NotNil(t, got)
		assert.Equal(t, "2", got["queueCount"])
		assert.Equal(t, "Standard", got["type"])
	})

	t.Run("FIFOQueues", func(t *testing.T) {
		t.Parallel()
		got := extractSQSConfig([]any{
			"https://sqs.us-east-1.amazonaws.com/123/demo-1.fifo",
			"https://sqs.us-east-1.amazonaws.com/123/demo-2.fifo",
		})
		require.NotNil(t, got)
		assert.Equal(t, "2", got["queueCount"])
		assert.Equal(t, "FIFO", got["type"])
	})

	t.Run("MixedFIFOAndStandardOmitsType", func(t *testing.T) {
		t.Parallel()
		got := extractSQSConfig([]any{
			"https://sqs.us-east-1.amazonaws.com/123/demo-1",
			"https://sqs.us-east-1.amazonaws.com/123/demo-2.fifo",
		})
		require.NotNil(t, got)
		assert.Equal(t, "2", got["queueCount"])
		_, hasType := got["type"]
		assert.False(t, hasType, "mixed FIFO/Standard should omit type")
	})

	t.Run("Envelope", func(t *testing.T) {
		t.Parallel()
		got := extractSQSConfig(map[string]any{
			"QueueUrls": []any{
				"https://sqs.us-east-1.amazonaws.com/123/demo",
			},
		})
		require.NotNil(t, got)
		assert.Equal(t, "1", got["queueCount"])
	})

	t.Run("NilInput", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractSQSConfig(nil))
	})

	t.Run("EmptyEnvelope", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractSQSConfig(map[string]any{"QueueUrls": []any{}}))
	})
}

// --- extractCognitoConfig ---

func TestExtractCognitoConfig(t *testing.T) {
	t.Parallel()

	t.Run("HappyPath_FlatSlice", func(t *testing.T) {
		t.Parallel()
		got := extractCognitoConfig([]any{
			map[string]any{
				"Id":     "us-east-1_abc",
				"Name":   "demo-pool",
				"Status": "Enabled",
			},
		})
		require.NotNil(t, got)
		assert.Equal(t, "1", got["userPoolCount"])
		assert.Equal(t, "demo-pool", got["poolName"])
		assert.Equal(t, "Enabled", got["status"])
		assert.Equal(t, "us-east-1_abc", got["poolId"])
	})

	t.Run("MultiplePools", func(t *testing.T) {
		t.Parallel()
		got := extractCognitoConfig([]any{
			map[string]any{"Id": "p1", "Name": "first"},
			map[string]any{"Id": "p2", "Name": "second"},
		})
		require.NotNil(t, got)
		assert.Equal(t, "2", got["userPoolCount"])
		assert.Equal(t, "first", got["poolName"]) // first one
	})

	t.Run("Envelope", func(t *testing.T) {
		t.Parallel()
		got := extractCognitoConfig(map[string]any{
			"UserPools": []any{
				map[string]any{"Id": "envID", "Name": "envName"},
			},
		})
		require.NotNil(t, got)
		assert.Equal(t, "1", got["userPoolCount"])
		assert.Equal(t, "envName", got["poolName"])
	})

	t.Run("NilInput", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractCognitoConfig(nil))
	})

	t.Run("EmptyEnvelope", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractCognitoConfig(map[string]any{"UserPools": []any{}}))
	})
}

// --- extractDynamoDBConfig ---

func TestExtractDynamoDBConfig(t *testing.T) {
	t.Parallel()

	t.Run("HappyPath_FlatSlice", func(t *testing.T) {
		t.Parallel()
		got := extractDynamoDBConfig([]any{"demo-table-1", "demo-table-2", "demo-table-3"})
		require.NotNil(t, got)
		assert.Equal(t, "3", got["tableCount"])
		assert.Equal(t, "demo-table-1", got["tableName"])
	})

	t.Run("Envelope", func(t *testing.T) {
		t.Parallel()
		got := extractDynamoDBConfig(map[string]any{
			"TableNames": []any{"only-table"},
		})
		require.NotNil(t, got)
		assert.Equal(t, "1", got["tableCount"])
		assert.Equal(t, "only-table", got["tableName"])
	})

	t.Run("StringSliceShape", func(t *testing.T) {
		t.Parallel()
		got := extractDynamoDBConfig([]string{"native-string-table"})
		require.NotNil(t, got)
		assert.Equal(t, "1", got["tableCount"])
		assert.Equal(t, "native-string-table", got["tableName"])
	})

	t.Run("NilInput", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractDynamoDBConfig(nil))
	})

	t.Run("EmptyEnvelope", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractDynamoDBConfig(map[string]any{"TableNames": []any{}}))
	})
}

// --- extractECSConfig ---

func TestExtractECSConfig(t *testing.T) {
	t.Parallel()

	t.Run("HappyPath_FlatSlice", func(t *testing.T) {
		t.Parallel()
		got := extractECSConfig([]any{
			map[string]any{
				"ClusterName": "demo-cluster",
				"ClusterArn":  "arn:aws:ecs:us-east-1:123:cluster/demo-cluster",
			},
			map[string]any{
				"ClusterName": "second-cluster",
				"ClusterArn":  "arn:aws:ecs:us-east-1:123:cluster/second-cluster",
			},
		})
		require.NotNil(t, got)
		assert.Equal(t, "2", got["clusterCount"])
		assert.Equal(t, "demo-cluster", got["clusterName"])
	})

	t.Run("Envelope", func(t *testing.T) {
		t.Parallel()
		got := extractECSConfig(map[string]any{
			"Clusters": []any{
				map[string]any{"ClusterName": "envelope-cluster"},
			},
		})
		require.NotNil(t, got)
		assert.Equal(t, "1", got["clusterCount"])
		assert.Equal(t, "envelope-cluster", got["clusterName"])
	})

	t.Run("NilInput", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractECSConfig(nil))
	})

	t.Run("EmptyEnvelope", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractECSConfig(map[string]any{"Clusters": []any{}}))
	})
}

// --- extractEKSConfig ---

func TestExtractEKSConfig(t *testing.T) {
	t.Parallel()

	t.Run("HappyPath_FlatSlice", func(t *testing.T) {
		t.Parallel()
		got := extractEKSConfig([]any{"demo-cluster", "second-cluster"})
		require.NotNil(t, got)
		assert.Equal(t, "2", got["clusterCount"])
		assert.Equal(t, "demo-cluster", got["clusterName"])
	})

	t.Run("Envelope", func(t *testing.T) {
		t.Parallel()
		got := extractEKSConfig(map[string]any{
			"Clusters": []any{"only-cluster"},
		})
		require.NotNil(t, got)
		assert.Equal(t, "1", got["clusterCount"])
		assert.Equal(t, "only-cluster", got["clusterName"])
	})

	t.Run("StringSlice", func(t *testing.T) {
		t.Parallel()
		got := extractEKSConfig([]string{"native"})
		require.NotNil(t, got)
		assert.Equal(t, "1", got["clusterCount"])
		assert.Equal(t, "native", got["clusterName"])
	})

	t.Run("NilInput", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractEKSConfig(nil))
	})

	t.Run("EmptyEnvelope", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractEKSConfig(map[string]any{"Clusters": []any{}}))
	})
}

// --- extractWAFConfig ---

func TestExtractWAFConfig(t *testing.T) {
	t.Parallel()

	t.Run("HappyPath_FlatSlice", func(t *testing.T) {
		t.Parallel()
		got := extractWAFConfig([]any{
			map[string]any{"Name": "demo-acl", "Id": "acl-1"},
			map[string]any{"Name": "second-acl", "Id": "acl-2"},
		})
		require.NotNil(t, got)
		assert.Equal(t, "2", got["webAclCount"])
		assert.Equal(t, "demo-acl", got["webAclName"])
		assert.Equal(t, "acl-1", got["webAclId"])
	})

	t.Run("Envelope", func(t *testing.T) {
		t.Parallel()
		got := extractWAFConfig(map[string]any{
			"WebACLs": []any{
				map[string]any{"Name": "env-acl", "Id": "env-id"},
			},
		})
		require.NotNil(t, got)
		assert.Equal(t, "1", got["webAclCount"])
		assert.Equal(t, "env-acl", got["webAclName"])
	})

	t.Run("NilInput", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractWAFConfig(nil))
	})

	t.Run("EmptyEnvelope", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractWAFConfig(map[string]any{"WebACLs": []any{}}))
	})
}

// --- extractCloudWatchLogsConfig ---

func TestExtractCloudWatchLogsConfig(t *testing.T) {
	t.Parallel()

	t.Run("UniformRetention", func(t *testing.T) {
		t.Parallel()
		got := extractCloudWatchLogsConfig([]any{
			map[string]any{
				"LogGroupName":    "/aws/lambda/demo-1",
				"RetentionInDays": float64(30),
			},
			map[string]any{
				"LogGroupName":    "/aws/lambda/demo-2",
				"RetentionInDays": float64(30),
			},
		})
		require.NotNil(t, got)
		assert.Equal(t, "2", got["logGroupCount"])
		assert.Equal(t, "30", got["retentionDays"])
		_, hasKMS := got["kmsEncrypted"]
		assert.False(t, hasKMS)
	})

	t.Run("MixedRetentionOmitted", func(t *testing.T) {
		t.Parallel()
		got := extractCloudWatchLogsConfig([]any{
			map[string]any{
				"LogGroupName":    "/aws/lambda/demo-1",
				"RetentionInDays": float64(30),
			},
			map[string]any{
				"LogGroupName":    "/aws/lambda/demo-2",
				"RetentionInDays": float64(7),
			},
		})
		require.NotNil(t, got)
		assert.Equal(t, "2", got["logGroupCount"])
		_, hasRet := got["retentionDays"]
		assert.False(t, hasRet, "mixed retention should omit retentionDays")
	})

	t.Run("KmsEncryptedAnyGroup", func(t *testing.T) {
		t.Parallel()
		got := extractCloudWatchLogsConfig([]any{
			map[string]any{"LogGroupName": "/aws/lambda/demo", "KmsKeyId": "arn:aws:kms:..."},
		})
		require.NotNil(t, got)
		assert.Equal(t, "Yes", got["kmsEncrypted"])
	})

	t.Run("Envelope", func(t *testing.T) {
		t.Parallel()
		got := extractCloudWatchLogsConfig(map[string]any{
			"LogGroups": []any{
				map[string]any{"LogGroupName": "/aws/lambda/env", "RetentionInDays": float64(14)},
			},
		})
		require.NotNil(t, got)
		assert.Equal(t, "1", got["logGroupCount"])
		assert.Equal(t, "14", got["retentionDays"])
	})

	t.Run("NoRetentionOnAnyGroup", func(t *testing.T) {
		t.Parallel()
		got := extractCloudWatchLogsConfig([]any{
			map[string]any{"LogGroupName": "/aws/lambda/demo"},
		})
		require.NotNil(t, got)
		assert.Equal(t, "1", got["logGroupCount"])
		_, hasRet := got["retentionDays"]
		assert.False(t, hasRet)
	})

	t.Run("NilInput", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractCloudWatchLogsConfig(nil))
	})

	t.Run("EmptyEnvelope", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, extractCloudWatchLogsConfig(map[string]any{"LogGroups": []any{}}))
	})
}

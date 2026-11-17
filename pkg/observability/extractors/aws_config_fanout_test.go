// Tests for the design config fields the AWS extractors read from the
// inspectors' config fan-out (#927). Fixtures are the JSON shapes the
// discovery/aws row types marshal to (the describe-queues,
// describe-tables and describe-clusters rows, ecsCluster, wafWebACL,
// kmsKeyEntry, the get-apis maps), plus a ListDistributions summary for
// CloudFront. The list-queues, list-tables and list-clusters string
// arrays must still give identity-only config.
//
// Each design key must read the same as the design value after
// composer.HumanizeFieldValue (the Go twin of reliable's
// humanizeConfigValue), or the card shows drift that is not there;
// TestFanOutKeys_HumanizeToDesignValues pins that.
package extractors

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	kmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer"
)

func TestExtractKMSConfig_CountsDistinctKeys(t *testing.T) {
	t.Parallel()
	got := extractKMSConfig([]any{
		map[string]any{"AliasName": "alias/demo-a", "TargetKeyId": "k1"},
		map[string]any{"AliasName": "alias/demo-a2", "TargetKeyId": "k1"},
		map[string]any{"AliasName": nil, "TargetKeyId": "k2", "KeyOnly": true},
	})
	require.NotNil(t, got)
	assert.Equal(t, "2", got["numKeys"], "two aliases of one key plus an unaliased key are two keys")
}

// TestExtractKMSConfig_AliasOnlyRows pins the list-aliases shape (and
// list-keys before #927): only alias rows, no KeyOnly row. numKeys is
// still a key count, the distinct aliased keys, not the alias count.
func TestExtractKMSConfig_AliasOnlyRows(t *testing.T) {
	t.Parallel()
	got := extractKMSConfig([]kmstypes.AliasListEntry{
		{AliasName: aws.String("alias/demo-a"), TargetKeyId: aws.String("k1")},
		{AliasName: aws.String("alias/demo-a2"), TargetKeyId: aws.String("k1")},
		{AliasName: aws.String("alias/demo-b"), TargetKeyId: aws.String("k2")},
	})
	assert.Equal(t, map[string]string{"numKeys": "2"}, got)
}

func TestExtractAPIGatewayConfig_CustomDomain(t *testing.T) {
	t.Parallel()
	got := extractAPIGatewayConfig([]any{
		map[string]any{"ApiId": "a0", "ProtocolType": "HTTP", "ApiEndpoint": "https://a0.execute-api.us-east-1.amazonaws.com"},
		map[string]any{
			"ApiId":            "a1",
			"ApiEndpoint":      "https://a1.execute-api.us-east-1.amazonaws.com",
			"CustomDomainName": "api.example.com",
			"CertificateArn":   "arn:aws:acm:us-east-1:111:certificate/abc",
		},
	})
	require.NotNil(t, got)
	assert.Equal(t, "api.example.com", got["domainName"], "the custom domain wins over the execute-api endpoint")
	assert.Equal(t, "arn:aws:acm:us-east-1:111:certificate/abc", got["certificateArn"])
}

func TestExtractCloudFrontConfig_TTLAndOriginPath(t *testing.T) {
	t.Parallel()
	dist := func(id string, ttl float64, path string) map[string]any {
		return map[string]any{
			"Id":                   id,
			"DefaultCacheBehavior": map[string]any{"TargetOriginId": "s3-origin", "DefaultTTL": ttl},
			"Origins": map[string]any{"Items": []any{
				map[string]any{"Id": "other", "OriginPath": "/ignored"},
				map[string]any{"Id": "s3-origin", "OriginPath": path},
			}},
		}
	}

	t.Run("Uniform", func(t *testing.T) {
		t.Parallel()
		got := extractCloudFrontConfig([]any{dist("E1", 3600, "/v1"), dist("E2", 3600, "/v1")})
		require.NotNil(t, got)
		assert.Equal(t, "3600", got["defaultTtl"])
		assert.Equal(t, "/v1", got["originPath"], "the default behavior's origin, not the first origin")
	})

	t.Run("EmptyOriginPathIsAValue", func(t *testing.T) {
		t.Parallel()
		got := extractCloudFrontConfig([]any{dist("E1", 0, "")})
		require.NotNil(t, got)
		v, ok := got["originPath"]
		assert.True(t, ok)
		assert.Equal(t, "", v)
		assert.Equal(t, "0", got["defaultTtl"])
	})

	t.Run("DisagreeingDistributionsOmit", func(t *testing.T) {
		t.Parallel()
		got := extractCloudFrontConfig([]any{dist("E1", 3600, "/v1"), dist("E2", 60, "/v2")})
		require.NotNil(t, got)
		assert.NotContains(t, got, "defaultTtl")
		assert.NotContains(t, got, "originPath")
	})

	t.Run("CachePolicyHasNoTTL", func(t *testing.T) {
		t.Parallel()
		got := extractCloudFrontConfig([]any{map[string]any{
			"Id":                   "E1",
			"DefaultCacheBehavior": map[string]any{"TargetOriginId": "o", "CachePolicyId": "p"},
		}})
		require.NotNil(t, got)
		assert.NotContains(t, got, "defaultTtl")
	})
}

func TestExtractSQSConfig_QueueRows(t *testing.T) {
	t.Parallel()

	t.Run("AttributesFetched", func(t *testing.T) {
		t.Parallel()
		got := extractSQSConfig([]any{
			map[string]any{"QueueUrl": "https://sqs/123/demo-a", "VisibilityTimeout": float64(600), "FifoQueue": false},
			map[string]any{"QueueUrl": "https://sqs/123/demo-b", "VisibilityTimeout": float64(600), "FifoQueue": false},
		})
		require.NotNil(t, got)
		assert.Equal(t, "2", got["queueCount"])
		assert.Equal(t, "Standard", got["type"])
		assert.Equal(t, "600", got["visibilityTimeout"])
	})

	t.Run("FifoAttributeAndSuffixFallback", func(t *testing.T) {
		t.Parallel()
		got := extractSQSConfig([]any{
			map[string]any{"QueueUrl": "https://sqs/123/demo-a.fifo", "VisibilityTimeout": float64(30), "FifoQueue": true},
			map[string]any{"QueueUrl": "https://sqs/123/demo-b.fifo"}, // describe failed: suffix decides
		})
		require.NotNil(t, got)
		assert.Equal(t, "FIFO", got["type"])
		assert.Equal(t, "30", got["visibilityTimeout"], "a queue without attributes neither hides nor fakes the value")
	})

	t.Run("DisagreeingTimeoutsOmit", func(t *testing.T) {
		t.Parallel()
		got := extractSQSConfig([]any{
			map[string]any{"QueueUrl": "https://sqs/123/a", "VisibilityTimeout": float64(30)},
			map[string]any{"QueueUrl": "https://sqs/123/b", "VisibilityTimeout": float64(600)},
		})
		require.NotNil(t, got)
		assert.NotContains(t, got, "visibilityTimeout")
	})
}

func TestExtractDynamoDBConfig_TableRows(t *testing.T) {
	t.Parallel()

	t.Run("OnDemandAWSOwnedKey", func(t *testing.T) {
		t.Parallel()
		got := extractDynamoDBConfig([]any{map[string]any{
			"TableName":             "demo-t",
			"BillingModeSummary":    map[string]any{"BillingMode": "PAY_PER_REQUEST"},
			"ProvisionedThroughput": map[string]any{"ReadCapacityUnits": float64(0), "WriteCapacityUnits": float64(0)},
		}})
		require.NotNil(t, got)
		assert.Equal(t, "demo-t", got["tableName"])
		assert.Equal(t, "On demand", got["type"], "design vocabulary, ZDdbType")
		assert.Equal(t, "AWS_OWNED", got["sseType"])
		assert.NotContains(t, got, "readCapacity", "on-demand tables have no capacity setting")
	})

	t.Run("ProvisionedWithoutBillingSummary", func(t *testing.T) {
		t.Parallel()
		got := extractDynamoDBConfig([]any{map[string]any{
			"TableName":             "demo-t",
			"ProvisionedThroughput": map[string]any{"ReadCapacityUnits": float64(5), "WriteCapacityUnits": float64(2)},
			"SSEDescription":        map[string]any{"Status": "ENABLED", "SSEType": "KMS"},
		}})
		require.NotNil(t, got)
		assert.Equal(t, "provisioned", got["type"])
		assert.Equal(t, "5", got["readCapacity"])
		assert.Equal(t, "2", got["writeCapacity"])
		assert.Equal(t, "KMS", got["sseType"])
	})

	t.Run("NotDescribedReportsIdentityOnly", func(t *testing.T) {
		t.Parallel()
		got := extractDynamoDBConfig([]any{map[string]any{"TableName": "demo-t"}})
		assert.Equal(t, map[string]string{"tableCount": "1", "tableName": "demo-t"}, got)
	})
}

func TestExtractECSConfig_DesignFields(t *testing.T) {
	t.Parallel()
	cluster := map[string]any{
		"ClusterName":       "demo-ecs",
		"Settings":          []any{map[string]any{"Name": "containerInsights", "Value": "enabled"}},
		"CapacityProviders": []any{"FARGATE", "FARGATE_SPOT"},
		"DefaultCapacityProviderStrategy": []any{
			map[string]any{"CapacityProvider": "FARGATE_SPOT", "Base": float64(0), "Weight": float64(4)},
			map[string]any{"CapacityProvider": "FARGATE", "Base": float64(1), "Weight": float64(1)},
		},
		"ServiceConnectEnabled": true,
	}
	got := extractECSConfig([]any{cluster})
	require.NotNil(t, got)
	assert.Equal(t, "true", got["enableContainerInsights"])
	assert.Equal(t, "FARGATE, FARGATE_SPOT", got["capacityProviders"])
	assert.Equal(t, "FARGATE", got["defaultCapacityProvider"], "largest base wins")
	assert.Equal(t, "true", got["enableServiceConnect"])

	t.Run("UnknownServiceConnectAndDisabledInsights", func(t *testing.T) {
		t.Parallel()
		got := extractECSConfig([]any{map[string]any{
			"ClusterName": "demo-ecs",
			"Settings":    []any{map[string]any{"Name": "containerInsights", "Value": "disabled"}},
		}})
		require.NotNil(t, got)
		assert.Equal(t, "false", got["enableContainerInsights"])
		assert.NotContains(t, got, "enableServiceConnect")
	})
}

// TestExtractECSConfig_ListClustersShape pins the list-clusters shape:
// the bare SDK clusters with no ServiceConnectEnabled field. The fields
// DescribeClusters returns still come out; enableServiceConnect, which
// only describe-clusters works out, is left out rather than guessed.
func TestExtractECSConfig_ListClustersShape(t *testing.T) {
	t.Parallel()
	got := extractECSConfig([]ecstypes.Cluster{{
		ClusterName:       aws.String("demo-ecs"),
		Settings:          []ecstypes.ClusterSetting{{Name: ecstypes.ClusterSettingNameContainerInsights, Value: aws.String("enhanced")}},
		CapacityProviders: []string{"FARGATE"},
	}})
	require.NotNil(t, got)
	assert.Equal(t, "demo-ecs", got["clusterName"])
	assert.Equal(t, "true", got["enableContainerInsights"])
	assert.Equal(t, "FARGATE", got["capacityProviders"])
	assert.NotContains(t, got, "enableServiceConnect")
}

func TestExtractEKSConfig_DesignFields(t *testing.T) {
	t.Parallel()
	got := extractEKSConfig([]any{map[string]any{
		"Name":                 "demo-eks",
		"Version":              "1.31",
		"EndpointPublicAccess": false,
		"NodeGroups": []any{map[string]any{
			"Name":          "ng",
			"InstanceTypes": []any{"m5.large"},
			"DesiredSize":   float64(2),
			"MinSize":       float64(1),
			"MaxSize":       float64(3),
		}},
	}})
	require.NotNil(t, got)
	assert.Equal(t, "demo-eks", got["clusterName"])
	assert.Equal(t, "1.31", got["version"])
	assert.Equal(t, "Private", got["controlPlaneVisibility"])
	assert.Equal(t, "2", got["desiredSize"])
	assert.Equal(t, "1", got["minSize"])
	assert.Equal(t, "3", got["maxSize"])
	assert.Equal(t, "m5.large", got["instanceType"])
	assert.NotContains(t, got, "haControlPlane")

	t.Run("NameOnlyRows", func(t *testing.T) {
		t.Parallel()
		got := extractEKSConfig([]any{map[string]any{"Name": "demo-eks"}})
		assert.Equal(t, map[string]string{"clusterCount": "1", "clusterName": "demo-eks"}, got)
	})
}

// TestListActionStrings_IdentityOnly pins that the list-queues,
// list-tables and list-clusters results, which stay bare string arrays,
// still give the identity-only config they gave before the describe
// actions existed.
func TestListActionStrings_IdentityOnly(t *testing.T) {
	t.Parallel()
	assert.Equal(t,
		map[string]string{"queueCount": "2", "type": "FIFO"},
		extractSQSConfig([]any{"https://sqs/1/a.fifo", "https://sqs/1/b.fifo"}))
	assert.Equal(t,
		map[string]string{"tableCount": "1", "tableName": "demo-t"},
		extractDynamoDBConfig([]any{"demo-t"}))
	assert.Equal(t,
		map[string]string{"clusterCount": "1", "clusterName": "demo-eks"},
		extractEKSConfig([]string{"demo-eks"}))
}

func TestExtractWAFConfig_GetWebACLFields(t *testing.T) {
	t.Parallel()
	got := extractWAFConfig([]any{map[string]any{
		"Name":          "demo-acl",
		"Id":            "acl-1",
		"Scope":         "REGIONAL",
		"DefaultAction": "ALLOW",
		"RuleNames":     []any{"AWSManagedRulesCommonRuleSet", "RateLimit"},
		"RuleCount":     float64(2),
	}})
	require.NotNil(t, got)
	assert.Equal(t, "REGIONAL", got["scope"])
	assert.Equal(t, "ALLOW", got["defaultAction"])
	assert.Equal(t, "2", got["ruleCount"])
	assert.Equal(t, "AWSManagedRulesCommonRuleSet, RateLimit", got["rules"])
}

// TestFanOutKeys_HumanizeToDesignValues checks that a deployed value
// reads the same as the design value it came from, once both go through
// the shared humanizer. The design values are the lib/stack/ir.ts enums.
func TestFanOutKeys_HumanizeToDesignValues(t *testing.T) {
	t.Parallel()
	cases := []struct {
		key, design, deployed string
	}{
		{"visibilityTimeout", "30 seconds", "30"},
		{"visibilityTimeout", "10 minutes", "600"},
		{"visibilityTimeout", "1 hour", "3600"},
		{"defaultTtl", "0", "0"},
		{"defaultTtl", "1h", "3600"},
		{"defaultTtl", "1day", "86400"},
		{"enableContainerInsights", "true", "true"},
		{"enableServiceConnect", "false", "false"},
		{"controlPlaneVisibility", "Private", "Private"},
		{"type", "On demand", "On demand"},
		{"type", "provisioned", "provisioned"},
		{"type", "FIFO", "FIFO"},
	}
	for _, tc := range cases {
		assert.Equal(t,
			composer.HumanizeFieldValue(tc.key, tc.design),
			composer.HumanizeFieldValue(tc.key, tc.deployed),
			"%s: design %q vs deployed %q", tc.key, tc.design, tc.deployed)
	}
}

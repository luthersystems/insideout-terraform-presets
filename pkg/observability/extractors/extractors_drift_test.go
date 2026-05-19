// Drift enforcement between the canonical AWS / GCP component-key
// inventory (composer.AllComponentKeys) and the live-config extractor
// dispatch in Extract. Fires if:
//
//  1. a key is declared in AllComponentKeys but has no case in the
//     Extract switch AND is not on the allowlist
//     (TestExtractCoversAllAWSComponents,
//     TestExtractCoversAllGCPComponents);
//  2. an allowlist entry references a key that no longer exists
//     (TestConfigExtractorAllowlist_OnlyKnownKeys);
//  3. an allowlist entry omits a rationale
//     (TestConfigExtractorAllowlist_HasRationale).
//
// Pattern lifted verbatim from the InsideOut backend
// internal/agentapi/config_extractors_drift_test.go (#204 port).

package extractors

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer"
	"github.com/luthersystems/insideout-terraform-presets/pkg/observability"
)

// configExtractorAllowlist is the set of component keys that
// deliberately have no live-config extractor. Every entry MUST
// reference a rationale so the allowlist doesn't silently accrete
// orphan entries.
//
// AWS — placeholders (aws_bastion, aws_grafana, aws_codepipeline):
// mapped to ec2.describe-instances in ComponentMetricsMapping as a
// stand-in with no dedicated SDK shape of their own. Sharing
// extractEC2Config here would leak the whole-region EC2 state into
// cards that aren't about general compute; better to return nil and
// let the UI use design values.
//
// Rationales prefixed with `[placeholder]` are AWS keys that share a
// stand-in shape (ec2.describe-instances) rather than having their
// own SDK contract; they're skipped by the metrics-only consistency
// check because they're a different class of allowlist entry.
//
// AWS keys with no metric surface at all (aws_backups,
// aws_github_actions, aws_eks_nodegroup) also live here — they have
// no inspector dispatcher and never will, so a real extractor isn't
// applicable. Same `[no-inspector]` prefix so the consistency check
// can spot them.
//
// GCP — every GCP key currently has either a real extractor or a
// `[no-inspector]` rationale (the GCP equivalent of placeholder for
// keys with no list-* dispatch in the InsideOut backend).
var configExtractorAllowlist = map[string]string{
	"aws_bastion":        "[placeholder] mapped to ec2.describe-instances, no dedicated shape (#204)",
	"aws_grafana":        "[placeholder] mapped to ec2.describe-instances, no dedicated shape (#204)",
	"aws_codepipeline":   "[placeholder] mapped to ec2.describe-instances, no dedicated shape (#204)",
	"aws_backups":        "[no-inspector] AWS Backup vaults aren't inspected; covered via tag-based discovery (#204)",
	"aws_github_actions": "[no-inspector] GitHub Actions IAM roles only — no SDK shape to extract (#204)",
	// #596: route53/acm dispatchers ship in pkg/observability/discovery/aws
	// (route53.go, acm.go) but no extractor lands yet — the panel
	// surfaces inspector output as raw SDK shapes until per-key field
	// extraction stabilizes. Tracked as a discovery-pipeline follow-up.
	"aws_route53": "[no-inspector] Route 53 dispatcher landed in #596 (route53.go); per-component extractor (config map for panel) deferred until SDK-shape extractor design settles for one-off resources",
	"aws_acm":     "[no-inspector] ACM dispatcher landed in #596 (acm.go); per-component extractor deferred until cert-specific field reads (status, days_to_expiry, validation state) are designed",

	// EKS node group: ComponentKey "aws_eks_nodegroup" doesn't have its
	// own SDK inspector — extraction is handled by aws_eks (#204, #224).
	"aws_eks_nodegroup": "[no-inspector] EKS node group is covered by the aws_eks inspector (#204)",

	"aws_sagemaker": "[no-inspector] SageMaker Studio inspector deferred (#615 explicitly marks discovery inspector as optional/follow-up — domain + execution role are surfaced via name-prefix scoping until per-resource extractors land)",

	"gcp_backups":      "[no-inspector] GCP Backup vaults aren't inspected; covered via label-based discovery (#204)",
	"gcp_cloud_deploy": "[no-inspector] Cloud Deploy delivery-pipeline inspector deferred (#613 explicitly marks discovery inspector + extractor as optional/follow-up); ComponentMetricsMapping has no entry yet so the panel falls back to design values until the per-component extractor lands",
	"gcp_cloud_dns":    "[no-inspector] Cloud DNS dispatcher landed in #596 (gcp/dns.go); per-component extractor (config map for panel) deferred pending zone-detail field reads (visibility, dnssec_config, record-count summary)",
}

// extractorFixtures are minimal SDK-shape fixtures that exercise each
// extractor's happy path. Keys are ComponentKey strings; values are
// raw `any` shapes matching what the inspector for that key returns.
// Used by TestExtractCoversAllAWSComponents to prove each key's
// dispatch case produces a non-nil config.
//
// Keeping these tiny on purpose — the per-extractor field-coverage
// suite (port follow-up) exercises field coverage; this drift test
// just proves the switch is wired.
var extractorFixtures = map[string]any{
	"aws_rds": map[string]any{"DBInstances": []any{
		map[string]any{"DBInstanceClass": "db.t3.micro"},
	}},
	"aws_ec2": map[string]any{"Reservations": []any{
		map[string]any{"Instances": []any{
			map[string]any{"InstanceType": "t3.small", "State": map[string]any{"Name": "running"}},
		}},
	}},
	"aws_elasticache": map[string]any{"CacheClusters": []any{
		map[string]any{"CacheNodeType": "cache.t3.micro"},
	}},
	"aws_opensearch": []any{
		map[string]any{
			"DomainName":    "demo",
			"EngineVersion": "OpenSearch_2.11",
			"ClusterConfig": map[string]any{"InstanceType": "r6g.large.search"},
		},
	},
	"aws_lambda": map[string]any{"Functions": []any{
		map[string]any{"Runtime": "go1.x", "MemorySize": float64(128), "Timeout": float64(10)},
	}},
	"aws_msk": map[string]any{"ClusterInfoList": []any{
		map[string]any{
			"BrokerNodeGroupInfo": map[string]any{"InstanceType": "kafka.m5.large"},
			"NumberOfBrokerNodes": float64(3),
		},
	}},
	"aws_alb": []any{
		map[string]any{"LoadBalancerName": "demo-alb", "Type": "application"},
	},
	"aws_kms": []any{
		map[string]any{"AliasName": "alias/demo", "TargetKeyId": "abc"},
	},
	"aws_s3": []any{
		map[string]any{"Name": "demo-bucket"},
	},
	"aws_secretsmanager": []any{
		map[string]any{"Name": "demo/secret", "ARN": "arn:..."},
	},
	"aws_vpc": []any{
		map[string]any{"VpcId": "vpc-1", "CidrBlock": "10.0.0.0/16"},
	},
	"aws_bedrock": []any{
		map[string]any{"Kind": "IAMRole", "RoleName": "demo-bedrock-role"},
	},
	"aws_cloudfront": []any{
		map[string]any{"Id": "E123", "DomainName": "d1.cloudfront.net"},
	},
	"aws_sqs":        []any{"https://sqs.us-east-1.amazonaws.com/123/demo"},
	"aws_apigateway": []any{map[string]any{"ApiId": "abc", "Name": "demo", "ProtocolType": "HTTP"}},
	"aws_cognito":    []any{map[string]any{"Id": "us-east-1_abc", "Name": "demo-pool"}},
	"aws_dynamodb":   []any{"demo-table"},
	"aws_ecs": []any{
		map[string]any{"ClusterName": "demo-cluster", "ClusterArn": "arn:aws:ecs:us-east-1:123:cluster/demo-cluster"},
	},
	"aws_eks": []any{"demo-cluster"},
	"aws_waf": []any{map[string]any{"Name": "demo", "Id": "acl-1"}},
	"aws_cloudwatch_logs": []any{
		map[string]any{"LogGroupName": "/aws/lambda/demo", "RetentionInDays": float64(30)},
	},
	"aws_cloudwatch_monitoring": []any{
		map[string]any{"LogGroupName": "/aws/lambda/demo", "RetentionInDays": float64(30)},
	},

	"gcp_compute": []any{
		map[string]any{
			"name":        "demo-vm",
			"machineType": "projects/demo/zones/us-central1-a/machineTypes/e2-medium",
			"zone":        "projects/demo/zones/us-central1-a",
			"status":      "RUNNING",
		},
	},
	"gcp_gke": []any{
		map[string]any{
			"name":                 "demo-gke",
			"status":               "RUNNING",
			"location":             "us-central1",
			"currentNodeCount":     float64(3),
			"currentMasterVersion": "1.29.4-gke.1043000",
			"autopilot":            map[string]any{"enabled": true},
		},
	},
	"gcp_cloud_run": []any{
		map[string]any{
			"name": "projects/demo/locations/us-central1/services/demo-svc",
			"uri":  "https://demo-svc-abc123-uc.a.run.app",
			"template": map[string]any{
				"containers": []any{map[string]any{
					"resources": map[string]any{
						"limits": map[string]any{"cpu": "1", "memory": "512Mi"},
					},
				}},
				"scaling": map[string]any{
					"minInstanceCount": float64(0),
					"maxInstanceCount": float64(10),
				},
				"maxInstanceRequestConcurrency": float64(80),
			},
		},
	},
	"gcp_memorystore": []any{
		map[string]any{
			"name":         "projects/demo/locations/us-central1/instances/demo-redis",
			"tier":         "BASIC",
			"memorySizeGb": float64(1),
			"redisVersion": "REDIS_7_0",
			"state":        "READY",
			"locationId":   "us-central1-a",
		},
	},
	"gcp_cloudsql": []any{
		map[string]any{
			"name":            "demo-postgres",
			"databaseVersion": "POSTGRES_15",
			"state":           "RUNNABLE",
			"region":          "us-central1",
			"settings": map[string]any{
				"tier":             "db-custom-2-7680",
				"dataDiskSizeGb":   float64(20),
				"availabilityType": "REGIONAL",
			},
		},
	},
	"gcp_gcs": []any{
		map[string]any{
			"name":         "demo-bucket",
			"location":     "US-CENTRAL1",
			"storageClass": "STANDARD",
			"created":      "2026-03-01T00:00:00Z",
		},
	},
	"gcp_firestore": []any{"users", "orders"},
	"gcp_pubsub": []any{
		map[string]any{
			"name":                     "projects/demo/topics/events",
			"messageRetentionDuration": "604800s",
		},
	},
	"gcp_cloud_kms": []any{
		map[string]any{
			"name":       "projects/demo/locations/global/keyRings/demo-kr",
			"createTime": "2026-03-01T00:00:00Z",
		},
	},
	"gcp_secret_manager": []any{
		map[string]any{
			"name":        "projects/demo/secrets/demo-secret",
			"replication": map[string]any{"automatic": map[string]any{}},
		},
	},
	"gcp_cloud_armor": []any{
		map[string]any{
			"name":  "demo-policy",
			"type":  "CLOUD_ARMOR",
			"rules": []any{map[string]any{"priority": float64(1000)}},
		},
	},
	"gcp_identity_platform": []any{
		map[string]any{
			"name":                  "projects/demo/tenants/demo-tenant",
			"displayName":           "Demo Tenant",
			"allowPasswordSignup":   true,
			"enableEmailLinkSignin": false,
			"mfaConfig":             map[string]any{"state": "ENABLED"},
		},
	},
	"gcp_vpc": []any{
		map[string]any{
			"name":                  "demo-vpc",
			"autoCreateSubnetworks": false,
			"routingConfig":         map[string]any{"routingMode": "REGIONAL"},
			"subnetworks":           []any{"https://www.googleapis.com/compute/v1/projects/demo/regions/us-central1/subnetworks/demo-sub"},
		},
	},
	"gcp_loadbalancer": []any{
		map[string]any{
			"name":           "demo-urlmap",
			"defaultService": "projects/demo/global/backendServices/demo-backend",
			"hostRules":      []any{map[string]any{"hosts": []any{"example.com"}}},
		},
	},
	"gcp_cloud_logging": []any{
		"projects/demo/logs/cloudaudit.googleapis.com%2Factivity",
		"projects/demo/logs/run.googleapis.com%2Fstdout",
	},
	"gcp_cloud_build": []any{
		map[string]any{
			"name":     "projects/demo/triggers/deploy-on-main",
			"filename": "cloudbuild.yaml",
			"github":   map[string]any{"owner": "luthersystems", "name": "the InsideOut backend"},
			"disabled": false,
		},
	},
	"gcp_vertex_ai": []any{
		map[string]any{
			"name":           "projects/demo/locations/us-central1/endpoints/demo-endpoint",
			"displayName":    "Demo Endpoint",
			"deployedModels": []any{map[string]any{"id": "m1"}},
		},
	},
	"gcp_cloud_monitoring": []any{
		map[string]any{
			"name":        "projects/demo/alertPolicies/abc123",
			"displayName": "High Error Rate",
			"enabled":     true,
		},
	},
	"gcp_cloud_functions": []any{
		map[string]any{
			"name":        "projects/demo/locations/us-central1/functions/demo-fn",
			"state":       "ACTIVE",
			"buildConfig": map[string]any{"runtime": "go122"},
		},
	},
	"gcp_api_gateway": []any{
		map[string]any{
			"name":        "projects/demo/locations/global/apis/demo-api",
			"displayName": "Demo API",
			"state":       "ACTIVE",
		},
	},
	"gcp_bastion": []any{
		map[string]any{
			"name":        "demo-bastion",
			"machineType": "projects/demo/zones/us-central1-a/machineTypes/e2-small",
			"zone":        "projects/demo/zones/us-central1-a",
			"status":      "RUNNING",
		},
	},
	// #606: gcp_github_actions extractor reads the list-workload-identity-
	// pools envelope. Inspector returns []*iam.WorkloadIdentityPool which
	// marshals to a top-level array; the extractor's sliceFromEnvelope
	// auto-detects the slice shape (envelope-key path also tolerated).
	"gcp_github_actions": []any{
		map[string]any{
			"name":        "projects/demo/locations/global/workloadIdentityPools/github",
			"displayName": "GitHub Actions",
			"state":       "ACTIVE",
			"disabled":    false,
		},
	},
}

// extractorExpectedFieldCount is the per-GCP-key exact field count
// the happy-path fixture in extractorFixtures should produce. Used by
// TestExtractCoversAllGCPComponents to trap typos on single-field
// reads in lightweight extractors. AWS extractors are not included
// here — many of them don't unconditionally emit a count, so a typo
// returns nil and the NotNil assertion catches it.
var extractorExpectedFieldCount = map[string]int{
	"gcp_compute":           5,
	"gcp_gke":               7,
	"gcp_cloud_run":         9,
	"gcp_memorystore":       7,
	"gcp_cloudsql":          8,
	"gcp_gcs":               4,
	"gcp_firestore":         2,
	"gcp_pubsub":            3,
	"gcp_cloud_kms":         2,
	"gcp_secret_manager":    3,
	"gcp_cloud_armor":       4,
	"gcp_identity_platform": 6,
	"gcp_vpc":               5,
	"gcp_loadbalancer":      4,
	"gcp_cloud_logging":     2,
	"gcp_cloud_build":       5,
	"gcp_vertex_ai":         5,
	"gcp_cloud_monitoring":  4,
	"gcp_cloud_functions":   4,
	"gcp_api_gateway":       4,
	"gcp_bastion":           5,
	"gcp_github_actions":    5, // poolCount + poolName + displayName + state + disabled
}

// TestExtractCoversAllAWSComponents asserts that every AWS component
// key declared in AllComponentKeys either (a) has a dispatch case in
// Extract that produces a non-nil result for a valid SDK-shape input,
// or (b) appears in configExtractorAllowlist with a rationale.
func TestExtractCoversAllAWSComponents(t *testing.T) {
	t.Parallel()
	for _, key := range awsKeys() {
		t.Run(string(key), func(t *testing.T) {
			t.Parallel()
			ks := string(key)
			if reason, ok := configExtractorAllowlist[ks]; ok {
				t.Skipf("allowlisted: %s", reason)
			}
			fixture, ok := extractorFixtures[ks]
			if !assert.True(t, ok,
				"AWS component key %q has no fixture in extractorFixtures. Either add an "+
					"entry with a minimal SDK-shape input, or add %q to configExtractorAllowlist "+
					"with a rationale.", ks, ks) {
				return
			}
			cfg := Extract(key, fixture)
			assert.NotNil(t, cfg,
				"Extract(%q) returned nil for a valid fixture. Wire a dispatch case in "+
					"extractors.go or add %q to configExtractorAllowlist with a rationale.",
				ks, ks)
		})
	}
}

// TestExtractCoversAllGCPComponents is the GCP mirror, with the
// stricter exact-field-count assertion to catch typos in lightweight
// extractors.
func TestExtractCoversAllGCPComponents(t *testing.T) {
	t.Parallel()
	for _, key := range gcpKeys() {
		t.Run(string(key), func(t *testing.T) {
			t.Parallel()
			ks := string(key)
			if reason, ok := configExtractorAllowlist[ks]; ok {
				t.Skipf("allowlisted: %s", reason)
			}
			fixture, ok := extractorFixtures[ks]
			if !assert.True(t, ok,
				"GCP component key %q has no fixture in extractorFixtures. Either add an "+
					"entry with a minimal proto-JSON-shape input, or add %q to "+
					"configExtractorAllowlist with a rationale.", ks, ks) {
				return
			}
			cfg := Extract(key, fixture)
			assert.NotNil(t, cfg,
				"Extract(%q) returned nil for a valid fixture. Wire a dispatch case in "+
					"extractors.go or add %q to configExtractorAllowlist with a rationale.",
				ks, ks)
			expected, ok := extractorExpectedFieldCount[ks]
			if !assert.Truef(t, ok,
				"GCP key %q has no entry in extractorExpectedFieldCount. Add the expected "+
					"field count next to the fixture so a typo on any single field read trips "+
					"this test.", ks) {
				return
			}
			assert.Equalf(t, expected, len(cfg),
				"Extract(%q) emitted %d field(s); expected exactly %d. Either a field-read "+
					"typo in extract%sConfig (production bug — fix the read), or a deliberate "+
					"field add/remove (update extractorExpectedFieldCount[%q] in the same "+
					"change). Got: %v",
				ks, len(cfg), expected, ks, ks, cfg)
		})
	}
}

// TestConfigExtractorAllowlist_OnlyForGetMetricsOrPlaceholders
// catches drift where a list-* dispatcher gets added but the
// allowlist entry isn't removed, or where a key is allowlisted but
// already has a list-* action.
func TestConfigExtractorAllowlist_OnlyForGetMetricsOrPlaceholders(t *testing.T) {
	t.Parallel()
	for key, reason := range configExtractorAllowlist {
		if strings.HasPrefix(reason, "[placeholder]") || strings.HasPrefix(reason, "[no-inspector]") {
			continue
		}
		t.Run(key, func(t *testing.T) {
			t.Parallel()
			mapping, ok := observability.ComponentMetricsMapping[composer.ComponentKey(key)]
			if !assert.True(t, ok,
				"allowlisted key %q is not in ComponentMetricsMapping; remove the allowlist "+
					"entry or add the key to AllComponentKeys + ComponentMetricsMapping.", key) {
				return
			}
			assert.Equalf(t, "get-metrics", mapping.Action,
				"allowlisted key %q has ComponentMetricsMapping action %q (not get-metrics). "+
					"Remove the allowlist entry and add a real extractor + fixture, OR change "+
					"ComponentMetricsMapping[%q].Action back to get-metrics if the list-* "+
					"dispatcher was added by mistake.",
				key, mapping.Action, key)
		})
	}
}

// TestConfigExtractorAllowlist_OnlyKnownKeys prevents the allowlist
// from shielding typos.
func TestConfigExtractorAllowlist_OnlyKnownKeys(t *testing.T) {
	t.Parallel()
	known := make(map[string]bool, len(composer.AllComponentKeys))
	for _, k := range composer.AllComponentKeys {
		known[string(k)] = true
	}
	for key := range configExtractorAllowlist {
		t.Run(key, func(t *testing.T) {
			t.Parallel()
			assert.True(t, known[key],
				"configExtractorAllowlist[%q] references a key that is NOT in "+
					"AllComponentKeys. Remove the stale allowlist entry.", key)
		})
	}
}

// TestConfigExtractorAllowlist_HasRationale keeps every allowlist row
// self-explanatory.
func TestConfigExtractorAllowlist_HasRationale(t *testing.T) {
	t.Parallel()
	for key, reason := range configExtractorAllowlist {
		assert.NotEmpty(t, reason,
			"configExtractorAllowlist[%q] must have a non-empty rationale.", key)
	}
}

// awsKeys returns the AWS subset of composer.AllComponentKeys.
func awsKeys() []composer.ComponentKey {
	var out []composer.ComponentKey
	for _, k := range composer.AllComponentKeys {
		if composer.CloudFor(k) == "aws" {
			out = append(out, k)
		}
	}
	return out
}

// gcpKeys returns the GCP subset of composer.AllComponentKeys.
func gcpKeys() []composer.ComponentKey {
	var out []composer.ComponentKey
	for _, k := range composer.AllComponentKeys {
		if composer.CloudFor(k) == "gcp" {
			out = append(out, k)
		}
	}
	return out
}

package imported

import (
	"regexp"
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer"
	"github.com/luthersystems/insideout-terraform-presets/pkg/imported/bindings"
	"github.com/luthersystems/insideout-terraform-presets/pkg/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type nonChartableObservableSurface struct {
	Class  string
	Reason string
}

// observableParityNonChartable records managed components with imported
// inventory coverage but no chartable metrics surface. These are not parity
// gaps: the component detail panel can discover the top-level resource, but
// there is no reliable DefaultMetrics binding to synthesize. If a real metrics
// binding lands for one of these tfTypes, TestObservableParity_NonChartableRowsAreStillNonChartable
// fails until the row is removed.
var observableParityNonChartable = map[composer.ComponentKey]nonChartableObservableSurface{
	composer.KeyAWSAppRunner: {
		Class:  "inventory-only",
		Reason: "#712 - aws_apprunner_service uses apprunner.list-services inventory; no default imported chart metrics",
	},
	composer.KeyAWSGrafana: {
		Class:  "placeholder",
		Reason: "#712 - aws_grafana_workspace is not an imported supported type while the managed preset is still a placeholder",
	},
	composer.KeyAWSSageMaker: {
		Class:  "inventory-only",
		Reason: "#712 - aws_sagemaker_domain uses sagemaker.list-domains inventory; no default imported chart metrics",
	},
	composer.KeyAWSBedrockAgent: {
		Class:  "inventory-only",
		Reason: "#762 - aws_bedrockagent_agent uses bedrock.list-agents inventory; no default imported chart metrics binding (imported discovery support is a follow-up, cf. #712)",
	},
	composer.KeyAWSAgentCoreGateway: {
		Class:  "inventory-only",
		Reason: "#763 - aws_bedrockagentcore_gateway uses agentcore.list-gateways inventory; the live AWS/Bedrock-AgentCore CloudWatch panel ships, but there is no default imported chart metrics binding yet (imported discovery support is a follow-up, cf. #712)",
	},
	composer.KeyAWSKendra: {
		Class:  "inventory-only",
		Reason: "#760 - aws_kendra_index uses kendra.list-indices inventory; the live AWS/Kendra CloudWatch panel ships, but there is no default imported chart metrics binding yet (imported discovery support is a follow-up, cf. #712)",
	},
	composer.KeyGCPCloudDeploy: {
		Class:  "inventory-only",
		Reason: "#712 - google_clouddeploy_delivery_pipeline uses clouddeploy.list-delivery-pipelines inventory; no default imported chart metrics",
	},
	composer.KeyGCPGitHubActions: {
		Class:  "security-inventory-only",
		Reason: "#712 - google_iam_workload_identity_pool is a security/IAM artifact, not a chartable time-series surface",
	},
}

func TestPrimaryTFTypeForComponent_KnownPairs(t *testing.T) {
	t.Parallel()

	cases := map[composer.ComponentKey]string{
		composer.KeyAWSS3:               "aws_s3_bucket",
		composer.KeyAWSCognito:          "aws_cognito_user_pool",
		composer.KeyGCPGCS:              "google_storage_bucket",
		composer.KeyGCPIdentityPlatform: "google_identity_platform_config",
	}
	for key, want := range cases {
		got, ok := PrimaryTFTypeForComponent(key)
		require.Truef(t, ok, "PrimaryTFTypeForComponent(%q) ok=false", key)
		assert.Equal(t, want, got)
	}
}

func TestManagedComponentPrimaryTFTypes_ReturnsCopy(t *testing.T) {
	t.Parallel()

	got := ManagedComponentPrimaryTFTypes()
	require.GreaterOrEqual(t, len(got), 50, "expected full managed/imported bridge surface")
	got[composer.KeyAWSS3] = "mutated"

	tfType, ok := PrimaryTFTypeForComponent(composer.KeyAWSS3)
	require.True(t, ok)
	assert.Equal(t, "aws_s3_bucket", tfType, "caller mutation must not affect package registry")
}

func TestObservableParity_ManagedMetricsHaveImportedCoverage(t *testing.T) {
	t.Parallel()

	require.GreaterOrEqualf(t, len(observability.ComponentMetricsMapping), 40,
		"observability.ComponentMetricsMapping has only %d entries; expected the full managed metrics surface",
		len(observability.ComponentMetricsMapping))

	for key := range observability.ComponentMetricsMapping {
		key := key
		t.Run(string(key), func(t *testing.T) {
			t.Parallel()

			tfType, ok := PrimaryTFTypeForComponent(key)
			require.Truef(t, ok,
				"managed metrics key %q has no primary imported tfType; add it to pkg/imported.ManagedComponentPrimaryTFTypes or allowlist with an issue ref",
				key)

			if surface, nonChartable := observableParityNonChartable[key]; nonChartable {
				assert.NotEmptyf(t, surface.Class, "observableParityNonChartable[%q] must classify the non-chartable surface", key)
				assert.NotEmptyf(t, surface.Reason, "observableParityNonChartable[%q] must explain the non-chartable surface", key)
				return
			}

			binding, ok := bindings.Binding(tfType)
			require.Truef(t, ok,
				"managed metrics key %q maps to imported tfType %q, but bindings.Binding(%q) has no registration",
				key, tfType, tfType)
			assert.NotEmptyf(t, binding.DefaultMetrics,
				"managed metrics key %q maps to imported tfType %q, but the binding has no DefaultMetrics and is not chartable",
				key, tfType)
		})
	}
}

func TestObservableParity_NonChartableRowsHaveIssue(t *testing.T) {
	t.Parallel()

	issueRef := regexp.MustCompile(`#\d{2,}`)
	for key, surface := range observableParityNonChartable {
		assert.Regexp(t, issueRef, surface.Reason,
			"observableParityNonChartable[%q] must reference a tracking issue", key)
	}
}

func TestObservableParity_RequestedInventoryOnlyRows(t *testing.T) {
	t.Parallel()

	for _, key := range []composer.ComponentKey{
		composer.KeyAWSAppRunner,
		composer.KeyAWSSageMaker,
		composer.KeyGCPCloudDeploy,
	} {
		surface, ok := observableParityNonChartable[key]
		require.Truef(t, ok, "expected %q to be classified as non-chartable inventory-only", key)
		assert.Equalf(t, "inventory-only", surface.Class, "expected %q to be inventory-only", key)
	}
}

func TestObservableParity_NonChartableRowsHaveManagedDispatch(t *testing.T) {
	t.Parallel()

	for key, surface := range observableParityNonChartable {
		_, ok := observability.ComponentMetricsMapping[key]
		assert.Truef(t, ok,
			"observableParityNonChartable[%q] is not in ComponentMetricsMapping; remove the stale classification", key)
		_, ok = PrimaryTFTypeForComponent(key)
		assert.Truef(t, ok,
			"observableParityNonChartable[%q] has no primary imported tfType; fix the map or remove the stale classification", key)
		switch surface.Class {
		case "inventory-only", "security-inventory-only", "placeholder":
		default:
			t.Fatalf("observableParityNonChartable[%q] has unknown class %q", key, surface.Class)
		}
	}
}

func TestObservableParity_NonChartableRowsAreStillNonChartable(t *testing.T) {
	t.Parallel()

	for key := range observableParityNonChartable {
		key := key
		t.Run(string(key), func(t *testing.T) {
			t.Parallel()

			tfType, ok := PrimaryTFTypeForComponent(key)
			if !ok {
				return
			}
			binding, ok := bindings.Binding(tfType)
			if ok && len(binding.DefaultMetrics) > 0 {
				t.Fatalf("observableParityNonChartable[%q] is stale: %q is now chartable; remove the non-chartable classification", key, tfType)
			}
		})
	}
}

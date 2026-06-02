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

var observableParityKnownGaps = map[composer.ComponentKey]string{
	composer.KeyAWSAppRunner:     "#712 - aws_apprunner_service has no imported metrics binding yet",
	composer.KeyAWSGrafana:       "#712 - aws_grafana_workspace is not an imported supported type while the managed preset is still a placeholder",
	composer.KeyAWSSageMaker:     "#712 - aws_sagemaker_domain has no imported metrics binding yet",
	composer.KeyGCPCloudDeploy:   "#712 - google_clouddeploy_delivery_pipeline has no imported metrics binding yet",
	composer.KeyGCPGitHubActions: "#712 - google_iam_workload_identity_pool is a security/IAM artifact, not a chartable time-series surface",
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

func TestObservableParity_ManagedMetricsHaveChartableImport(t *testing.T) {
	t.Parallel()

	require.GreaterOrEqualf(t, len(observability.ComponentMetricsMapping), 40,
		"observability.ComponentMetricsMapping has only %d entries; expected the full managed metrics surface",
		len(observability.ComponentMetricsMapping))

	for key := range observability.ComponentMetricsMapping {
		key := key
		t.Run(string(key), func(t *testing.T) {
			t.Parallel()

			if reason, gap := observableParityKnownGaps[key]; gap {
				t.Skipf("known gap: %s", reason)
			}

			tfType, ok := PrimaryTFTypeForComponent(key)
			require.Truef(t, ok,
				"managed metrics key %q has no primary imported tfType; add it to pkg/imported.ManagedComponentPrimaryTFTypes or allowlist with an issue ref",
				key)

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

func TestObservableParity_KnownGapsHaveIssue(t *testing.T) {
	t.Parallel()

	issueRef := regexp.MustCompile(`#\d{2,}`)
	for key, reason := range observableParityKnownGaps {
		assert.Regexp(t, issueRef, reason,
			"observableParityKnownGaps[%q] must reference a tracking issue", key)
	}
}

func TestObservableParity_KnownGapsAreManagedMetrics(t *testing.T) {
	t.Parallel()

	for key := range observableParityKnownGaps {
		_, ok := observability.ComponentMetricsMapping[key]
		assert.Truef(t, ok,
			"observableParityKnownGaps[%q] is not in ComponentMetricsMapping; remove the stale allowlist row", key)
		_, ok = PrimaryTFTypeForComponent(key)
		assert.Truef(t, ok,
			"observableParityKnownGaps[%q] has no primary imported tfType; fix the map or remove the stale row", key)
	}
}

func TestObservableParity_KnownGapsAreStillGaps(t *testing.T) {
	t.Parallel()

	for key := range observableParityKnownGaps {
		key := key
		t.Run(string(key), func(t *testing.T) {
			t.Parallel()

			tfType, ok := PrimaryTFTypeForComponent(key)
			if !ok {
				return
			}
			binding, ok := bindings.Binding(tfType)
			if ok && len(binding.DefaultMetrics) > 0 {
				t.Fatalf("observableParityKnownGaps[%q] is stale: %q is now chartable; remove the allowlist row", key, tfType)
			}
		})
	}
}

package awsdiscover

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported"
)

// fakeByIDEnricher is a minimal type that satisfies ByIDEnricher.
// It exists only to back the compile-time `var _ ByIDEnricher`
// assertion below — no production code implements ByIDEnricher yet
// (Phase 2 enricher rollout PRs add real impls one per type).
type fakeByIDEnricher struct{}

func (fakeByIDEnricher) ResourceType() string { return "aws_test_fake" }

func (fakeByIDEnricher) EnrichByID(ctx context.Context, identity *imported.ResourceIdentity, clients EnrichClients) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}

// Compile-time assertion. If ByIDEnricher's shape drifts, this fails
// at build time. This is the load-bearing shape lock — no runtime
// "fake calls itself" test is needed because there is nothing to
// observe beyond the compile.
var _ ByIDEnricher = (*fakeByIDEnricher)(nil)

// TestExistingEnrichersDoNotImplementByID pins the per-type
// ByIDEnricher implementation status against the REAL production
// registration in NewAWSDiscoverer. As Phase 2 PRs add real
// EnrichByID impls, the allowlist must shrink in lockstep with the
// production registration. A production-only change (add a
// ByIDEnricher impl, forget to update allowlist; or vice versa) fails
// the test loud. A regression that drops the registration entirely is
// caught by the explicit wantTotal size check below.
func TestExistingEnrichersDoNotImplementByID(t *testing.T) {
	// Empty aws.Config is safe — the constructor only stores closures
	// and per-type discoverer/enricher structs; no SDK calls fire.
	d := NewAWSDiscoverer(aws.Config{})

	// Allowlist: types whose enrichers explicitly DO NOT implement
	// ByIDEnricher yet. Shrink as Phase 2 rollout lands per-type impls.
	notImplemented := map[string]bool{
		"aws_dynamodb_table": true,
	}

	// Fail-fast: pin the expected total byTypeEnricher size so a
	// silent drop (or duplicate-key squashing) in production fails the
	// test. The expected total = hand-rolled enrichers (see
	// handRolledTypes below) + every type in cloudControlTypeConfigs
	// that doesn't have a hand-rolled override. The latter is computed
	// at test time so an addition to cloudControlTypeConfigs doesn't
	// silently flow into the production enricher coverage without a
	// deliberate test update.
	handRolled := 19
	ccOverrides := 0
	handRolledTypes := map[string]bool{
		"aws_apigatewayv2_stage":                             true,
		"aws_autoscaling_group_tag":                          true,
		"aws_bedrock_guardrail":                              true,
		"aws_bedrock_model_invocation_logging_configuration": true,
		"aws_cloudwatch_log_group":                           true,
		"aws_dynamodb_contributor_insights":                  true,
		"aws_dynamodb_table":                                 true,
		"aws_iam_role_policy_attachment":                     true,
		"aws_resourceexplorer2_index":                        true,
		"aws_resourceexplorer2_view":                         true,
		"aws_s3_bucket":                                      true,
		"aws_s3_bucket_lifecycle_configuration":              true,
		"aws_s3_bucket_ownership_controls":                   true,
		"aws_s3_bucket_public_access_block":                  true,
		"aws_s3_bucket_server_side_encryption_configuration": true,
		"aws_s3_bucket_versioning":                           true,
		"aws_secretsmanager_secret":                          true,
		"aws_service_discovery_private_dns_namespace":        true,
		"aws_wafv2_web_acl_association":                      true,
	}
	for _, ccCfg := range cloudControlTypeConfigs {
		if handRolledTypes[ccCfg.TFType] {
			ccOverrides++
		}
	}
	wantTotal := handRolled + len(cloudControlTypeConfigs) - ccOverrides
	if got := len(d.byTypeEnricher); got != wantTotal {
		t.Errorf("byTypeEnricher size = %d, want %d (production registration drifted from test)", got, wantTotal)
	}

	for tfType, enr := range d.byTypeEnricher {
		_, implementsByID := enr.(ByIDEnricher)
		expectNot := notImplemented[tfType]
		switch {
		case expectNot && implementsByID:
			t.Errorf("%s: enricher now implements ByIDEnricher — remove from notImplemented allowlist", tfType)
		case !expectNot && !implementsByID:
			t.Errorf("%s: enricher must implement ByIDEnricher (not in notImplemented allowlist)", tfType)
		}
	}
}

// TestCloudControlEnricherCoversEveryCCRoutedType asserts that every
// TF type in cloudControlTypeConfigs has a registered AttributeEnricher
// in NewAWSDiscoverer.byTypeEnricher — either a hand-rolled override
// (4 types today) or a generic cloudControlEnricher. A regression that
// drops the cloudControlEnricher wiring loop in NewAWSDiscoverer would
// silently strip Cloud Control coverage from ~91 types; this test
// catches that as a per-type miss rather than waiting for a downstream
// integration test to surface the regression.
func TestCloudControlEnricherCoversEveryCCRoutedType(t *testing.T) {
	d := NewAWSDiscoverer(aws.Config{})
	for _, ccCfg := range cloudControlTypeConfigs {
		if _, ok := d.byTypeEnricher[ccCfg.TFType]; !ok {
			t.Errorf("cloudcontrol-routed TFType %q has no registered AttributeEnricher", ccCfg.TFType)
		}
	}
}

// TestCloudControlEnricherSkipsHandRolledOverrides asserts the
// override-wins invariant in NewAWSDiscoverer's wiring loop: for every
// TF type that has a hand-rolled enricher AND a cloudControlTypeConfigs
// entry, the registered enricher must be the hand-rolled one (not the
// generic cloudControlEnricher). A silent regression that flips the
// override order would replace the higher-fidelity hand-rolled payloads
// with the lower-fidelity Cloud Control payloads (the PoC quantified
// the quality gap at 57% on log groups).
func TestCloudControlEnricherSkipsHandRolledOverrides(t *testing.T) {
	d := NewAWSDiscoverer(aws.Config{})
	// Mirrors the hand-rolled set in NewAWSDiscoverer.byTypeEnricher.
	// Kept as a literal slice rather than a reflection-based scan so
	// adding a new hand-rolled enricher requires an explicit update
	// here — the next reviewer sees the intent in the test diff.
	handRolled := []string{
		"aws_apigatewayv2_stage",
		"aws_cloudwatch_log_group",
		"aws_dynamodb_contributor_insights",
		"aws_dynamodb_table",
		"aws_iam_role_policy_attachment",
		"aws_resourceexplorer2_index",
		"aws_resourceexplorer2_view",
		"aws_s3_bucket",
		"aws_s3_bucket_lifecycle_configuration",
		"aws_s3_bucket_ownership_controls",
		"aws_s3_bucket_public_access_block",
		"aws_s3_bucket_server_side_encryption_configuration",
		"aws_s3_bucket_versioning",
		"aws_secretsmanager_secret",
	}
	for _, tfType := range handRolled {
		enr, ok := d.byTypeEnricher[tfType]
		if !ok {
			t.Errorf("%s: missing from byTypeEnricher", tfType)
			continue
		}
		if _, isCC := enr.(*cloudControlEnricher); isCC {
			t.Errorf("%s: registered enricher is cloudControlEnricher, want hand-rolled override", tfType)
		}
	}
}

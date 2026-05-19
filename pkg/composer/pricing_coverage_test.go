package composer

// pricing_coverage_test.go gates the silent gap discovered during the
// #598 / #614 / #618 / #620 audit: PricingData.Components is a typed
// Go struct with explicit per-key fields, and #614 (gcp_cloud_deploy)
// + #618 (aws_sagemaker) landed without adding their fields. The cost-
// LLM is therefore structurally unable to emit a pricing row for those
// components — the customer's cost panel drops them silently. There
// was no coverage test to fail CI, so the gap was invisible.
//
// This test cross-references AllComponentKeys against the json tags
// on PricingData.Components. Every AWS / GCP key must have a matching
// field, or be listed in pricingDeferredKeys with an issue-tracked
// rationale. The allowlist forces deferrals to be acknowledged in
// code (matching the [no-inspector] pattern in extractors_drift_test.go).

import (
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// pricingDeferredKeys are AWS/GCP ComponentKeys whose PricingData.Components
// field is intentionally absent today. Adding a key here MUST cite the
// issue tracking the backfill. Deleting an entry without adding the
// field will make the test fail — that's the point.
//
// Trim back to {} once the corresponding backfill PR lands.
var pricingDeferredKeys = map[ComponentKey]string{
	KeyGCPCloudDeploy: "Backfill pricing row for gcp_cloud_deploy (#614 / tracked in #621). The cost-LLM cannot emit a Cloud Deploy line until PricingData.Components has a `GCPCloudDeploy *PricingItem` field.",
	KeyAWSSageMaker:   "Backfill pricing row for aws_sagemaker (#615 / #618 / tracked in #621). The cost-LLM cannot emit a SageMaker Studio line until PricingData.Components has an `AWSSageMaker *PricingItem` field.",
}

// pricingNonComponentKeys are AllComponentKeys entries that are NOT
// expected to have a pricing row — third-party toggles, conceptual
// classifiers, EKS node group (covered by the parent EKS row), etc.
var pricingNonComponentKeys = map[ComponentKey]bool{
	KeyAWSEKSNodeGroup: true, // priced under the parent KeyAWSEKS row
}

// pricingComponentsJSONTags returns the json tag (minus `,omitempty`)
// for every field on PricingData.Components, restricted to fields
// whose tag starts with "aws_" or "gcp_". Other fields (architecture,
// cloud, splunk, datadog, githubactions, legacy unprefixed) are
// outside the AllComponentKeys coverage scope.
func pricingComponentsJSONTags(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	pd := PricingData{}
	cv := reflect.ValueOf(pd).FieldByName("Components")
	require.True(t, cv.IsValid(), "PricingData.Components field must exist")
	ct := cv.Type()
	for i := 0; i < ct.NumField(); i++ {
		f := ct.Field(i)
		tag := f.Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		name := strings.TrimSuffix(strings.SplitN(tag, ",", 2)[0], ",omitempty")
		if !strings.HasPrefix(name, "aws_") && !strings.HasPrefix(name, "gcp_") {
			continue
		}
		out[name] = true
	}
	return out
}

// TestPricingDataCoversAllComponentKeys fails when an AWS or GCP
// ComponentKey in AllComponentKeys lacks a matching json tag on
// PricingData.Components.
//
// This catches the class of bug found in the #598-roll-up audit:
// preset PRs that wired the composer but forgot to add the pricing
// field, leaving the cost-LLM structurally unable to emit a row.
// The pricing struct's per-key fields are typed by design (the LLM
// schema generator emits a curated JSON Schema that the model is
// constrained to), so a missing field is a hard structural gap, not
// a soft "panel renders empty" fallback.
func TestPricingDataCoversAllComponentKeys(t *testing.T) {
	t.Parallel()

	got := pricingComponentsJSONTags(t)

	var missing []string
	for _, k := range AllComponentKeys {
		s := string(k)
		if !strings.HasPrefix(s, "aws_") && !strings.HasPrefix(s, "gcp_") {
			continue
		}
		if pricingNonComponentKeys[k] {
			continue
		}
		if _, ok := pricingDeferredKeys[k]; ok {
			// Deferred — pin the rationale is still present.
			require.NotEmptyf(t, pricingDeferredKeys[k],
				"pricingDeferredKeys[%q] must carry a non-empty issue-tracked rationale", k)
			continue
		}
		if !got[s] {
			missing = append(missing, s)
		}
	}
	require.Empty(t, missing,
		"PricingData.Components is missing fields for these AWS/GCP ComponentKeys: %v\n"+
			"Fix: add `<Key> *PricingItem `+\"`json:\\\"<key>,omitempty\\\"`\"+` to PricingData.Components in pricing.go,\n"+
			"OR add the key to pricingDeferredKeys with a tracked-issue rationale.",
		missing)

	// Sanity: an entry in pricingDeferredKeys must NOT also have a
	// corresponding field (would mean someone forgot to delete the
	// allowlist entry when they landed the backfill).
	for k := range pricingDeferredKeys {
		s := string(k)
		require.Falsef(t, got[s],
			"%q appears in pricingDeferredKeys AND in PricingData.Components — delete the allowlist entry; the backfill has landed",
			s)
	}
}

// TestPricingDataDeferralReferencesIssues guards against an empty-
// rationale allowlist entry. Adding to pricingDeferredKeys is a code
// signal that the deferral is intentional — the rationale field must
// reference the tracking issue so reviewers can find it.
func TestPricingDataDeferralReferencesIssues(t *testing.T) {
	t.Parallel()
	for k, reason := range pricingDeferredKeys {
		require.Truef(t, strings.Contains(reason, "#"),
			"pricingDeferredKeys[%q] must cite a tracking issue (e.g. \"#1234\") so the deferral is auditable",
			k)
	}
}

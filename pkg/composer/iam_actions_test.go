package composer

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestAWSIAMActions_CoverAllAWSKeys ensures every AWS-backed ComponentKey
// in AllComponentKeys has an entry (possibly nil) in AWSIAMActions. New AWS
// components added without an IAM entry break this test loudly — without
// it, ui-core's preflight would silently skip the new component's IAM
// requirements and let terraform apply fail at AWS instead.
//
// We walk AllComponentKeys rather than ModulePath because the former is
// the canonical "user-selectable component" registry: it includes the
// polymorphic KeyAWSEKSControlPlane / KeyAWSEKSNodeGroup keys (so
// preflight covers them), and excludes third-party toggles like
// KeySplunk / KeyDatadog that have no Luther-managed AWS IAM surface.
func TestAWSIAMActions_CoverAllAWSKeys(t *testing.T) {
	for _, k := range AllComponentKeys {
		if CloudFor(k) != "aws" {
			continue
		}
		_, ok := AWSIAMActions[k]
		assert.True(t, ok,
			"AWSIAMActions is missing %s — preflight will silently skip it; add an entry (nil is permitted) in pkg/composer/iam_actions.go",
			k)
	}
}

// TestAWSIAMActions_NoUnknownKeys ensures every key in AWSIAMActions
// resolves to a known AWS ComponentKey in AllComponentKeys. Catches typos
// and stale entries left behind after a key rename or removal.
func TestAWSIAMActions_NoUnknownKeys(t *testing.T) {
	known := allComponentKeysSet()
	for k := range AWSIAMActions {
		assert.True(t, known[k],
			"AWSIAMActions[%s] is not in AllComponentKeys — stale or typo'd key; remove or fix in pkg/composer/iam_actions.go",
			k)
		assert.Equal(t, "aws", CloudFor(k),
			"AWSIAMActions[%s] resolves to non-aws cloud — only AWS keys belong here",
			k)
	}
}

// TestGCPIAMPermissions_CoverAllGCPKeys is the GCP analog of
// TestAWSIAMActions_CoverAllAWSKeys.
func TestGCPIAMPermissions_CoverAllGCPKeys(t *testing.T) {
	for _, k := range AllComponentKeys {
		if CloudFor(k) != "gcp" {
			continue
		}
		_, ok := GCPIAMPermissions[k]
		assert.True(t, ok,
			"GCPIAMPermissions is missing %s — preflight will silently skip it; add an entry (nil is permitted) in pkg/composer/iam_actions.go",
			k)
	}
}

// TestGCPIAMPermissions_NoUnknownKeys is the GCP analog of
// TestAWSIAMActions_NoUnknownKeys.
func TestGCPIAMPermissions_NoUnknownKeys(t *testing.T) {
	known := allComponentKeysSet()
	for k := range GCPIAMPermissions {
		assert.True(t, known[k],
			"GCPIAMPermissions[%s] is not in AllComponentKeys — stale or typo'd key; remove or fix in pkg/composer/iam_actions.go",
			k)
		assert.Equal(t, "gcp", CloudFor(k),
			"GCPIAMPermissions[%s] resolves to non-gcp cloud — only GCP keys belong here",
			k)
	}
}

// allComponentKeysSet returns AllComponentKeys as a presence map for the
// reverse-direction drift-guard tests.
func allComponentKeysSet() map[ComponentKey]bool {
	out := make(map[ComponentKey]bool, len(AllComponentKeys))
	for _, k := range AllComponentKeys {
		out[k] = true
	}
	return out
}

// TestRequiredAWSIAMActions_AlwaysIncluded confirms the always-required set
// is returned even when no components are passed.
func TestRequiredAWSIAMActions_AlwaysIncluded(t *testing.T) {
	got := RequiredAWSIAMActions(nil)
	for _, want := range AlwaysRequiredAWSIAMActions {
		assert.Contains(t, got, want,
			"RequiredAWSIAMActions(nil) must include always-required action %q", want)
	}
}

// TestRequiredGCPIAMPermissions_AlwaysIncluded confirms the always-required
// set is returned even when no components are passed.
func TestRequiredGCPIAMPermissions_AlwaysIncluded(t *testing.T) {
	got := RequiredGCPIAMPermissions(nil)
	for _, want := range AlwaysRequiredGCPIAMPermissions {
		assert.Contains(t, got, want,
			"RequiredGCPIAMPermissions(nil) must include always-required permission %q", want)
	}
}

// TestRequiredAWSIAMActions_DedupsAcrossComponents verifies overlapping
// per-component entries collapse to a single output entry. KeyAWSBastion
// and KeyAWSEC2 both declare "ec2:RunInstances".
func TestRequiredAWSIAMActions_DedupsAcrossComponents(t *testing.T) {
	got := RequiredAWSIAMActions([]ComponentKey{KeyAWSBastion, KeyAWSEC2})
	count := 0
	for _, a := range got {
		if a == "ec2:RunInstances" {
			count++
		}
	}
	assert.Equal(t, 1, count,
		"ec2:RunInstances should appear exactly once in RequiredAWSIAMActions output, got %d (full output: %v)",
		count, got)
}

// TestRequiredGCPIAMPermissions_DedupsAcrossComponents verifies overlapping
// per-component entries collapse to a single output entry.
func TestRequiredGCPIAMPermissions_DedupsAcrossComponents(t *testing.T) {
	got := RequiredGCPIAMPermissions([]ComponentKey{KeyGCPVPC, KeyGCPVPC, KeyGCPCloudKMS, KeyGCPCloudKMS})
	count := 0
	for _, p := range got {
		if p == "cloudkms.keyRings.create" {
			count++
		}
	}
	assert.Equal(t, 1, count,
		"cloudkms.keyRings.create should appear exactly once in RequiredGCPIAMPermissions output, got %d (full output: %v)",
		count, got)
}

// TestRequiredAWSIAMActions_StableOrder verifies output is sorted, so
// SimulatePrincipalPolicy input is deterministic across runs.
func TestRequiredAWSIAMActions_StableOrder(t *testing.T) {
	got := RequiredAWSIAMActions([]ComponentKey{KeyAWSS3, KeyAWSVPC, KeyAWSKMS, KeyAWSALB})
	for i := 1; i < len(got); i++ {
		assert.LessOrEqual(t, got[i-1], got[i],
			"RequiredAWSIAMActions output must be sorted; %q > %q at index %d (full: %v)",
			got[i-1], got[i], i-1, got)
	}
}

// TestRequiredGCPIAMPermissions_StableOrder verifies output is sorted, so
// testIamPermissions input is deterministic across runs.
func TestRequiredGCPIAMPermissions_StableOrder(t *testing.T) {
	got := RequiredGCPIAMPermissions([]ComponentKey{KeyGCPGCS, KeyGCPCloudKMS, KeyGCPGKE, KeyGCPPubSub})
	for i := 1; i < len(got); i++ {
		assert.LessOrEqual(t, got[i-1], got[i],
			"RequiredGCPIAMPermissions output must be sorted; %q > %q at index %d (full: %v)",
			got[i-1], got[i], i-1, got)
	}
}

// TestRequiredAWSIAMActions_UnknownComponentIgnored verifies forward-compat:
// a future composer release introducing a new ComponentKey shouldn't break
// in-flight ui-core deploys that pass that key here.
func TestRequiredAWSIAMActions_UnknownComponentIgnored(t *testing.T) {
	const phantom ComponentKey = "aws_does_not_exist_yet"
	got := RequiredAWSIAMActions([]ComponentKey{phantom, KeyAWSS3})
	assert.Contains(t, got, "s3:CreateBucket",
		"RequiredAWSIAMActions should still return known-key actions when an unknown key is mixed in: %v",
		got)
	for _, a := range AlwaysRequiredAWSIAMActions {
		assert.Contains(t, got, a,
			"RequiredAWSIAMActions should still return always-required actions when an unknown key is mixed in: missing %q (full: %v)",
			a, got)
	}
}

// TestRequiredGCPIAMPermissions_UnknownComponentIgnored is the GCP analog.
func TestRequiredGCPIAMPermissions_UnknownComponentIgnored(t *testing.T) {
	const phantom ComponentKey = "gcp_does_not_exist_yet"
	got := RequiredGCPIAMPermissions([]ComponentKey{phantom, KeyGCPGCS})
	assert.Contains(t, got, "storage.buckets.create",
		"RequiredGCPIAMPermissions should still return known-key permissions when an unknown key is mixed in: %v",
		got)
	for _, p := range AlwaysRequiredGCPIAMPermissions {
		assert.Contains(t, got, p,
			"RequiredGCPIAMPermissions should still return always-required permissions when an unknown key is mixed in: missing %q (full: %v)",
			p, got)
	}
}

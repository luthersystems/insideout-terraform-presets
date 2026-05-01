package composer

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestGCPServices_CoverAllGCPKeys ensures every GCP-backed ComponentKey in
// AllComponentKeys has an entry (possibly nil) in GCPServices. New GCP
// components added without a service entry break this test loudly —
// without it, ui-core's preflight would silently skip the new component's
// service-enablement requirements and let terraform apply fail at GCP
// instead. Mirrors TestGCPIAMPermissions_CoverAllGCPKeys.
func TestGCPServices_CoverAllGCPKeys(t *testing.T) {
	for _, k := range AllComponentKeys {
		if CloudFor(k) != "gcp" {
			continue
		}
		_, ok := GCPServices[k]
		assert.True(t, ok,
			"GCPServices is missing %s — preflight will silently skip it; add an entry (nil is permitted) in pkg/composer/gcp_services.go",
			k)
	}
}

// TestGCPServices_NoUnknownKeys ensures every key in GCPServices resolves
// to a known GCP ComponentKey in AllComponentKeys. Catches typos and
// stale entries left behind after a key rename or removal. Mirrors
// TestGCPIAMPermissions_NoUnknownKeys.
func TestGCPServices_NoUnknownKeys(t *testing.T) {
	known := allComponentKeysSet()
	for k := range GCPServices {
		assert.True(t, known[k],
			"GCPServices[%s] is not in AllComponentKeys — stale or typo'd key; remove or fix in pkg/composer/gcp_services.go",
			k)
		assert.Equal(t, "gcp", CloudFor(k),
			"GCPServices[%s] resolves to non-gcp cloud — only GCP keys belong here",
			k)
	}
}

// TestGCPServices_EntryHasName ensures every non-nil service entry carries
// a non-empty Name. A blank Name would silently disable the preflight for
// that component.
func TestGCPServices_EntryHasName(t *testing.T) {
	for k, services := range GCPServices {
		for i, s := range services {
			assert.NotEmpty(t, s.Name,
				"GCPServices[%s][%d].Name is empty; serviceusage.batchGet would skip it",
				k, i)
		}
	}
	for i, s := range AlwaysRequiredGCPServices {
		assert.NotEmpty(t, s.Name,
			"AlwaysRequiredGCPServices[%d].Name is empty; serviceusage.batchGet would skip it",
			i)
	}
}

// TestRequiredGCPServices_AlwaysIncluded confirms the always-required set
// is returned even when no components are passed.
func TestRequiredGCPServices_AlwaysIncluded(t *testing.T) {
	got := RequiredGCPServices(nil)
	gotNames := make(map[string]bool, len(got))
	for _, s := range got {
		gotNames[s.Name] = true
	}
	for _, want := range AlwaysRequiredGCPServices {
		assert.True(t, gotNames[want.Name],
			"RequiredGCPServices(nil) must include always-required service %q (full output: %v)",
			want.Name, got)
	}
}

// TestRequiredGCPServices_DedupsAcrossComponents verifies overlapping
// per-component entries collapse to a single output entry.
// KeyGCPCloudFunctions and KeyGCPCloudBuild both declare
// "cloudbuild.googleapis.com". Mirrors
// TestRequiredGCPIAMPermissions_DedupsAcrossComponents and additionally
// covers duplicate keys in the input slice (caller passes [A, A, B, B]).
func TestRequiredGCPServices_DedupsAcrossComponents(t *testing.T) {
	got := RequiredGCPServices([]ComponentKey{
		KeyGCPCloudFunctions, KeyGCPCloudFunctions,
		KeyGCPCloudBuild, KeyGCPCloudBuild,
	})
	count := 0
	for _, s := range got {
		if s.Name == "cloudbuild.googleapis.com" {
			count++
		}
	}
	assert.Equal(t, 1, count,
		"cloudbuild.googleapis.com should appear exactly once in RequiredGCPServices output, got %d (full output: %v)",
		count, got)
}

// TestRequiredGCPServices_DedupPreservesTitle verifies the dedup keeps the
// declared Title for a service that appears under multiple components.
// Important because ui-core's missing-APIs banner shows Title to end users
// — a Title swap (e.g. to the empty string) would silently degrade the UI.
func TestRequiredGCPServices_DedupPreservesTitle(t *testing.T) {
	got := RequiredGCPServices([]ComponentKey{KeyGCPCloudFunctions, KeyGCPCloudBuild})
	for _, s := range got {
		if s.Name == "cloudbuild.googleapis.com" {
			assert.Equal(t, "Cloud Build", s.Title,
				"cloudbuild.googleapis.com Title should be preserved through dedup, got %q (full: %v)",
				s.Title, got)
			return
		}
	}
	t.Fatalf("cloudbuild.googleapis.com missing from output: %v", got)
}

// TestRequiredGCPServices_StableOrder verifies output is sorted by Name
// so serviceusage.batchGet input is deterministic across runs.
func TestRequiredGCPServices_StableOrder(t *testing.T) {
	got := RequiredGCPServices([]ComponentKey{KeyGCPGCS, KeyGCPCloudKMS, KeyGCPGKE, KeyGCPPubSub})
	for i := 1; i < len(got); i++ {
		assert.LessOrEqual(t, got[i-1].Name, got[i].Name,
			"RequiredGCPServices output must be sorted by Name; %q > %q at index %d (full: %v)",
			got[i-1].Name, got[i].Name, i-1, got)
	}
}

// TestRequiredGCPServices_UnknownComponentIgnored verifies forward-compat:
// a future composer release introducing a new ComponentKey shouldn't break
// in-flight ui-core deploys that pass that key here.
func TestRequiredGCPServices_UnknownComponentIgnored(t *testing.T) {
	const phantom ComponentKey = "gcp_does_not_exist_yet"
	got := RequiredGCPServices([]ComponentKey{phantom, KeyGCPGCS})
	gotNames := make(map[string]bool, len(got))
	for _, s := range got {
		gotNames[s.Name] = true
	}
	assert.True(t, gotNames["storage.googleapis.com"],
		"RequiredGCPServices should still return known-key services when an unknown key is mixed in: %v",
		got)
	for _, want := range AlwaysRequiredGCPServices {
		assert.True(t, gotNames[want.Name],
			"RequiredGCPServices should still return always-required services when an unknown key is mixed in: missing %q (full: %v)",
			want.Name, got)
	}
}

package dependencies

import (
	"testing"
)

// TestFieldRefs_KnownEntries pins the curated cross-ref map. This is
// the contract reliable (and any other per-instance consumer) resolves
// against — issue #667. A change here is a deliberate registry edit
// and must show up loudly in the diff; downstream golden / drift tests
// bump in lockstep.
func TestFieldRefs_KnownEntries(t *testing.T) {
	t.Parallel()
	want := map[string]string{
		"network":           "google_compute_network",
		"subnetwork":        "google_compute_subnetwork",
		"kms_key_name":      "google_kms_crypto_key",
		"role":              "aws_iam_role",
		"role_arn":          "aws_iam_role",
		"kms_key_arn":       "aws_kms_key",
		"kms_key_id":        "aws_kms_key",
		"kms_master_key_id": "aws_kms_key",
		"vpc_id":            "aws_vpc",
		"subnet_id":         "aws_subnet",
	}
	got := FieldRefs()
	if len(got) != len(want) {
		t.Fatalf("FieldRefs() has %d entries, want %d: %v", len(got), len(want), got)
	}
	for field, target := range want {
		if got[field] != target {
			t.Errorf("FieldRefs()[%q] = %q, want %q", field, got[field], target)
		}
	}
}

// TestFieldRefs_ReturnsCopy pins the defensive-copy contract: mutating
// the returned map must not affect the registry or other callers.
func TestFieldRefs_ReturnsCopy(t *testing.T) {
	t.Parallel()
	first := FieldRefs()
	first["vpc_id"] = "tampered"
	delete(first, "role")

	second := FieldRefs()
	if second["vpc_id"] != "aws_vpc" {
		t.Errorf("registry leaked a mutation: FieldRefs()[\"vpc_id\"] = %q, want %q", second["vpc_id"], "aws_vpc")
	}
	if _, ok := second["role"]; !ok {
		t.Error("registry leaked a deletion: FieldRefs()[\"role\"] missing after caller deleted its copy")
	}
}

// TestLookup_HitAndMiss pins the single-field resolution helper for
// both the hit and miss paths.
func TestLookup_HitAndMiss(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		field      string
		wantTarget string
		wantOK     bool
	}{
		{"hit_vpc_id", "vpc_id", "aws_vpc", true},
		{"hit_role", "role", "aws_iam_role", true},
		{"hit_subnetwork", "subnetwork", "google_compute_subnetwork", true},
		{"miss_id", "id", "", false},
		{"miss_arn", "arn", "", false},
		{"miss_empty", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotTarget, gotOK := Lookup(tc.field)
			if gotTarget != tc.wantTarget || gotOK != tc.wantOK {
				t.Errorf("Lookup(%q) = (%q, %t), want (%q, %t)", tc.field, gotTarget, gotOK, tc.wantTarget, tc.wantOK)
			}
		})
	}
}

// TestLookup_AgreesWithFieldRefs pins that the two accessors never
// diverge — every FieldRefs() entry resolves identically via Lookup.
func TestLookup_AgreesWithFieldRefs(t *testing.T) {
	t.Parallel()
	for field, target := range FieldRefs() {
		got, ok := Lookup(field)
		if !ok || got != target {
			t.Errorf("Lookup(%q) = (%q, %t), want (%q, true)", field, got, ok, target)
		}
	}
}

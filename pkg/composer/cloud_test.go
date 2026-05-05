package composer

import "testing"

// TestCloudFor pins the typed-key cloud derivation. Drive-by addition
// alongside CloudFromKeys; previously CloudFor's behaviour was only
// asserted indirectly via iam_actions_test.go, observability_moves_test.go,
// gcp_services_test.go, and preset_defaults_test.go.
func TestCloudFor(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		k    ComponentKey
		want string
	}{
		// Standard prefixed keys — the modern naming convention.
		{"aws_lambda", KeyAWSLambda, "aws"},
		{"gcp_vpc", KeyGCPVPC, "gcp"},
		// Legacy polymorphic AWS keys whose string identity predates the
		// cloud-prefixed vocabulary. The "default-aws" fallback exists
		// specifically for these — a regression that flipped the default
		// to "gcp" would mis-route their dispatch.
		{"legacy resource (EKS control plane / lambda)", KeyAWSEKSControlPlane, "aws"},
		{"legacy ec2 (EKS node group)", KeyAWSEKSNodeGroup, "aws"},
		// Empty key — defaults to "aws". Matches the documented behaviour.
		{"empty key", ComponentKey(""), "aws"},
		// Adversarial: the contract is "gcp_" prefix INCLUDING the
		// underscore. A regression flipping the check to `HasPrefix(s,
		// "gcp")` would mis-route any hypothetical key like "gcps3" or
		// "gcpus_lambda" to "gcp". No such keys exist today, but the
		// underscore is part of the wire contract — pin it.
		{"prefix without underscore is not gcp", ComponentKey("gcps3"), "aws"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := CloudFor(tc.k); got != tc.want {
				t.Errorf("CloudFor(%q) = %q, want %q", tc.k, got, tc.want)
			}
		})
	}
}

// TestCloudFromKeys pins the slice-shaped sibling of CloudFor used at
// reliable/mcp-server/server/svc/v2.go:2792 to derive the cloud from a
// session's selected component-key strings. Covers reliable's
// TestCloudFromKeys table at v2_test.go:1684 behaviorally — every
// branch reliable's table exercises (empty, all-aws, all-gcp, mixed,
// legacy unprefixed) is exercised here too — but is NOT a literal
// mirror; this table additionally covers nil, the all-empty-strings
// case, and both mixed-orderings to harden the lifted implementation.
// The contract pinned is the BEHAVIOR, not the exact fixture set:
// `gcp` iff any key has the `gcp_` prefix.
func TestCloudFromKeys(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		keys []string
		want string
	}{
		// Empty / nil — both default to "aws". A regression that returned
		// "" or "gcp" for the empty case would silently mis-route every
		// session whose components list is empty.
		{"nil slice", nil, "aws"},
		{"empty slice", []string{}, "aws"},
		// All AWS — uses both the modern "aws_*" prefix and the legacy
		// "ec2" / "resource" polymorphic keys to confirm neither flips
		// the cloud.
		{"all aws prefixed", []string{"aws_lambda", "aws_rds"}, "aws"},
		{"all aws legacy keys", []string{"resource", "ec2"}, "aws"},
		// Single GCP key alone or mixed with AWS keys must trigger
		// "gcp" — the function is "any-gcp wins", not "majority".
		{"all gcp prefixed", []string{"gcp_vpc", "gcp_cloudsql"}, "gcp"},
		{"mixed first gcp", []string{"gcp_vpc", "aws_lambda"}, "gcp"},
		{"mixed first aws then gcp", []string{"aws_lambda", "gcp_vpc"}, "gcp"},
		// Empty-string key — must not crash; treated as non-gcp so the
		// slice's other keys decide the cloud.
		{"empty string key only", []string{""}, "aws"},
		{"empty string key with gcp", []string{"", "gcp_vpc"}, "gcp"},
		// Adversarial: the contract is "gcp_" INCLUDING the underscore
		// (mirror of the TestCloudFor underscore-required row). A
		// regression flipping the check to `HasPrefix(k, "gcp")` would
		// mis-route any hypothetical key like "gcps3" to "gcp".
		{"prefix without underscore is not gcp", []string{"gcps3"}, "aws"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := CloudFromKeys(tc.keys); got != tc.want {
				t.Errorf("CloudFromKeys(%v) = %q, want %q", tc.keys, got, tc.want)
			}
		})
	}
}

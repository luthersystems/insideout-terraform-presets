package permissions

import (
	"encoding/json"
	"sort"
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/pkg/insideout-import/registry"
)

// awsTFTypeToServiceSlug mirrors awsdiscover.serviceSlugByTFType. We
// duplicate it here (rather than import the cmd-side map) so this
// pkg-level test stays free of cmd/ imports — the registry package
// follows the same posture for its parity tests. Drift between this
// table and the awsdiscover slug map will surface as a coverage-test
// failure: any TF type from the registry whose slug isn't keyed here
// (or whose slug isn't represented in aws.json) trips the assertion.
var awsTFTypeToServiceSlug = map[string]string{
	"aws_cloudwatch_log_group":  "cloudwatchlogs",
	"aws_dynamodb_table":        "dynamodb",
	"aws_iam_policy":            "iam_policy",
	"aws_iam_role":              "iam_role",
	"aws_kms_key":               "kms",
	"aws_lambda_function":       "lambda",
	"aws_s3_bucket":             "s3",
	"aws_secretsmanager_secret": "secretsmanager",
	"aws_sqs_queue":             "sqs",
}

func TestLoadAWSManifest_ParsesAndIsNonEmpty(t *testing.T) {
	t.Parallel()

	m, err := LoadAWSManifest()
	if err != nil {
		t.Fatalf("LoadAWSManifest: %v", err)
	}
	if m.Version != 1 {
		t.Errorf("Version: got %d, want 1", m.Version)
	}
	if m.Provider != "aws" {
		t.Errorf("Provider: got %q, want %q", m.Provider, "aws")
	}
	if len(m.Permissions) == 0 {
		t.Fatal("Permissions: empty slice; aws.json must declare at least one entry per discoverer")
	}
}

func TestLoadGCPManifest_ParsesAndIsNonEmpty(t *testing.T) {
	t.Parallel()

	m, err := LoadGCPManifest()
	if err != nil {
		t.Fatalf("LoadGCPManifest: %v", err)
	}
	if m.Version != 1 {
		t.Errorf("Version: got %d, want 1", m.Version)
	}
	if m.Provider != "gcp" {
		t.Errorf("Provider: got %q, want %q", m.Provider, "gcp")
	}
	if len(m.Permissions) == 0 {
		t.Fatal("Permissions: empty slice; gcp.json must declare at least one entry")
	}
}

// TestAWSManifest_CoversAllAWSDiscovererServices asserts every service
// slug derived from registry.SupportedDiscoverTypes("aws") appears at
// least once in the AWS manifest. Adding a new TF type to the registry
// (and therefore a new per-service discoverer) without updating aws.json
// fails this test — the credential probe in reliable would silently
// under-test the new service otherwise.
func TestAWSManifest_CoversAllAWSDiscovererServices(t *testing.T) {
	t.Parallel()

	m, err := LoadAWSManifest()
	if err != nil {
		t.Fatalf("LoadAWSManifest: %v", err)
	}

	slugInManifest := make(map[string]bool, len(m.Permissions))
	for _, p := range m.Permissions {
		slugInManifest[p.Service] = true
	}

	tfTypes := registry.SupportedDiscoverTypes(registry.ProviderAWS)
	if len(tfTypes) == 0 {
		t.Fatal("registry.SupportedDiscoverTypes(aws) returned empty; manifest coverage cannot be checked")
	}
	for _, tfType := range tfTypes {
		slug, ok := awsTFTypeToServiceSlug[tfType]
		if !ok {
			t.Errorf("awsTFTypeToServiceSlug missing entry for %q (registry exposes it but the test slug map does not); update awsTFTypeToServiceSlug AND aws.json", tfType)
			continue
		}
		if !slugInManifest[slug] {
			t.Errorf("aws.json: missing permission entry for service slug %q (TF type %q); add at least one IAM action row", slug, tfType)
		}
	}

	// sts is a one-call-per-run dependency that isn't tied to a TF
	// type via the registry, but the credential probe still needs it
	// — gate explicitly so the manifest can't drop sts:GetCallerIdentity.
	if !slugInManifest["sts"] {
		t.Error("aws.json: missing sts entry; sts:GetCallerIdentity is called once per discover run to stamp AccountID")
	}
}

// TestGCPManifest_CoversCloudAssetInventory asserts the single
// cloud_asset_inventory row that powers every GCP discoverer is present.
// If a future PR breaks GCP into per-API discoverers, this test should
// be updated alongside that change rather than removed wholesale.
func TestGCPManifest_CoversCloudAssetInventory(t *testing.T) {
	t.Parallel()

	m, err := LoadGCPManifest()
	if err != nil {
		t.Fatalf("LoadGCPManifest: %v", err)
	}
	for _, p := range m.Permissions {
		if p.Service == "cloud_asset_inventory" {
			if p.GCPRole != "roles/cloudasset.viewer" {
				t.Errorf("cloud_asset_inventory: GCPRole=%q, want roles/cloudasset.viewer", p.GCPRole)
			}
			if p.IAMPermission != "cloudasset.assets.searchAllResources" {
				t.Errorf("cloud_asset_inventory: IAMPermission=%q, want cloudasset.assets.searchAllResources", p.IAMPermission)
			}
			return
		}
	}
	t.Error("gcp.json: missing cloud_asset_inventory entry; SearchAllResources is the only API every GCP discoverer calls")
}

// TestAWSManifest_DeterministicSortedByServiceAndAction protects the
// byte-stability invariant: the on-disk JSON must be sorted by
// (service, iam_action) so a downstream byte-comparison probe (e.g.
// reliable's CI gate that checksums the embedded blob) doesn't churn
// when an unrelated PR touches the file. A regression that re-orders
// entries without re-sorting fails this test.
func TestAWSManifest_DeterministicSortedByServiceAndAction(t *testing.T) {
	t.Parallel()

	m, err := LoadAWSManifest()
	if err != nil {
		t.Fatalf("LoadAWSManifest: %v", err)
	}

	want := make([]Permission, len(m.Permissions))
	copy(want, m.Permissions)
	sort.SliceStable(want, func(i, j int) bool {
		if want[i].Service != want[j].Service {
			return want[i].Service < want[j].Service
		}
		return want[i].IAMAction < want[j].IAMAction
	})
	for i := range m.Permissions {
		if m.Permissions[i] != want[i] {
			t.Fatalf("aws.json entry %d unsorted: got (service=%q, action=%q), want (service=%q, action=%q); re-sort the file by (service, iam_action)",
				i, m.Permissions[i].Service, m.Permissions[i].IAMAction, want[i].Service, want[i].IAMAction)
		}
	}
}

// TestAWSManifest_NoUnknownPurposeStrings asserts every entry carries a
// non-empty Purpose. Reliable's wizard surfaces Purpose verbatim when
// explaining a missing-permission failure to the operator; an empty
// string would render a blank explanation row.
func TestAWSManifest_NoUnknownPurposeStrings(t *testing.T) {
	t.Parallel()

	m, err := LoadAWSManifest()
	if err != nil {
		t.Fatalf("LoadAWSManifest: %v", err)
	}
	for i, p := range m.Permissions {
		if p.Purpose == "" {
			t.Errorf("aws.json entry %d (service=%q, action=%q): empty Purpose", i, p.Service, p.IAMAction)
		}
	}
}

// TestGCPManifest_NoUnknownPurposeStrings is the GCP-side mirror of the
// purpose-non-empty contract.
func TestGCPManifest_NoUnknownPurposeStrings(t *testing.T) {
	t.Parallel()

	m, err := LoadGCPManifest()
	if err != nil {
		t.Fatalf("LoadGCPManifest: %v", err)
	}
	for i, p := range m.Permissions {
		if p.Purpose == "" {
			t.Errorf("gcp.json entry %d (service=%q, role=%q): empty Purpose", i, p.Service, p.GCPRole)
		}
	}
}

// TestManifests_RoundTripJSON sanity-checks that loaded manifests
// re-marshal to valid JSON without losing fields. Catches struct-tag
// typos that would silently drop a field on either the read or write
// path.
func TestManifests_RoundTripJSON(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		load func() (Manifest, error)
	}{
		{"aws", LoadAWSManifest},
		{"gcp", LoadGCPManifest},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m, err := tc.load()
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			b, err := json.Marshal(m)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var round Manifest
			if err := json.Unmarshal(b, &round); err != nil {
				t.Fatalf("unmarshal round-trip: %v", err)
			}
			if round.Version != m.Version || round.Provider != m.Provider {
				t.Errorf("round-trip drift: got %+v, want %+v", round, m)
			}
			if len(round.Permissions) != len(m.Permissions) {
				t.Errorf("round-trip permission count: got %d, want %d", len(round.Permissions), len(m.Permissions))
			}
		})
	}
}

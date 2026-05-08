package imported

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/pkg/insideout-import/registry"
	"github.com/stretchr/testify/require"
)

// TestCategory_KnownTypeReturnsExpected pins the canonical mapping
// table-driven. A regression that drops or rewires a row surfaces here
// with a single concrete-row failure rather than a noisy whole-map diff.
func TestCategory_KnownTypeReturnsExpected(t *testing.T) {
	t.Parallel()
	cases := []struct {
		tfType string
		want   string
	}{
		// AWS — one row per category to pin the wire-format strings.
		{"aws_sqs_queue", "Events"},
		{"aws_dynamodb_table", "Data Storage"},
		{"aws_lambda_function", "Virtual Machines"},
		{"aws_secretsmanager_secret", "Security"},
		{"aws_cloudwatch_log_group", "Observability"},
		{"aws_iam_role", "Security"},
		{"aws_iam_policy", "Security"},
		{"aws_kms_key", "Security"},
		{"aws_s3_bucket", "Data Storage"},
		{"aws_vpc", "Network Security"},
		{"aws_subnet", "Network Security"},
		{"aws_security_group", "Network Security"},
		{"aws_rds_cluster", "Data Storage"},
		{"aws_rds_instance", "Data Storage"},
		{"aws_eks_cluster", "Virtual Machines"},
		{"aws_lb", "Network Security"},
		{"aws_elb", "Network Security"},
		{"aws_route53_zone", "Network Security"},
		{"aws_cloudfront_distribution", "Network Security"},
		{"aws_ecs_cluster", "Virtual Machines"},
		{"aws_ecr_repository", "Data Storage"},

		// GCP — same coverage shape.
		{"google_pubsub_topic", "Events"},
		{"google_pubsub_subscription", "Events"},
		{"google_storage_bucket", "Data Storage"},
		{"google_secret_manager_secret", "Security"},
		{"google_compute_network", "Network Security"},
		{"google_compute_instance", "Virtual Machines"},
		{"google_compute_disk", "Data Storage"},
		{"google_compute_subnetwork", "Network Security"},
		{"google_compute_firewall", "Network Security"},
		{"google_sql_database_instance", "Data Storage"},
		{"google_container_cluster", "Virtual Machines"},
		{"google_service_account", "Security"},
		{"google_cloudfunctions_function", "Virtual Machines"},
		{"google_cloud_run_service", "Virtual Machines"},
		{"google_bigquery_dataset", "Data Storage"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.tfType, func(t *testing.T) {
			t.Parallel()
			got := Category(tc.tfType)
			if got != tc.want {
				t.Errorf("Category(%q) = %q, want %q", tc.tfType, got, tc.want)
			}
		})
	}
}

// TestCategory_UnknownTypeReturnsEmpty pins the fallthrough contract.
// The reliable wizard treats "" as "render under Other"; a regression
// that returned a default string would silently mis-categorize new
// resource types until the operator complained.
func TestCategory_UnknownTypeReturnsEmpty(t *testing.T) {
	t.Parallel()
	cases := []string{
		"",
		"aws_brand_new_thing_2099",
		"google_unknown",
		"random_string",
		"not_a_terraform_type_at_all",
	}
	for _, tt := range cases {
		tt := tt
		t.Run(tt, func(t *testing.T) {
			t.Parallel()
			got := Category(tt)
			if got != "" {
				t.Errorf("Category(%q) = %q, want \"\" for unknown type", tt, got)
			}
		})
	}
}

// TestCategory_GoldenSnapshot pins the full canonical mapping against
// testdata/category.golden. Re-seed with `UPDATE_GOLDEN=1 go test
// ./pkg/composer/imported/ -run TestCategory_GoldenSnapshot`. The
// golden carries one tab-separated `<tftype>\t<category>\n` row per
// entry, sorted by key — keeps the diff at PR review time
// alphabetical and free of category-grouping reflow.
func TestCategory_GoldenSnapshot(t *testing.T) {
	goldenPath := filepath.Join("testdata", "category.golden")
	current := snapshotCategoryMap()

	if os.Getenv("UPDATE_GOLDEN") == "1" {
		require.NoError(t, os.MkdirAll(filepath.Dir(goldenPath), 0o755))
		require.NoError(t, os.WriteFile(goldenPath, []byte(current), 0o644))
		t.Logf("wrote golden: %s", goldenPath)
		return
	}

	want, err := os.ReadFile(goldenPath)
	require.NoError(t, err,
		"golden missing — run `UPDATE_GOLDEN=1 go test ./pkg/composer/imported/ -run TestCategory_GoldenSnapshot`")
	require.Equal(t, string(want), current,
		"category map drifted from %s. If this is intentional, re-seed via UPDATE_GOLDEN=1.",
		goldenPath)
}

// TestCategory_TotalOverDiscoverRegistry asserts the category map is a
// superset of registry.SupportedDiscoverTypes for every supported
// provider. Adding a new type to the discover registry without
// categorizing it lands here as a per-type failure that names exactly
// which row to add.
func TestCategory_TotalOverDiscoverRegistry(t *testing.T) {
	t.Parallel()
	for _, provider := range registry.SupportedProviders() {
		provider := provider
		t.Run(provider, func(t *testing.T) {
			t.Parallel()
			for _, tfType := range registry.SupportedDiscoverTypes(provider) {
				if got := Category(tfType); got == "" {
					t.Errorf("provider=%s tfType=%q has no Category mapping; add it to pkg/composer/imported/category.go and re-seed the golden",
						provider, tfType)
				}
			}
		})
	}
}

// TestCategory_StableValuesPinWireFormat fences off the literal
// category strings. Any change to these is a wire-format break with
// the reliable wizard's DiscoveredResource.group field.
func TestCategory_StableValuesPinWireFormat(t *testing.T) {
	t.Parallel()
	require.Equal(t, "Events", CategoryEvents)
	require.Equal(t, "Data Storage", CategoryDataStorage)
	require.Equal(t, "Network Security", CategoryNetworkSecurity)
	require.Equal(t, "Observability", CategoryObservability)
	require.Equal(t, "Security", CategorySecurity)
	require.Equal(t, "Virtual Machines", CategoryVirtualMachines)
}

// TestCategoriesReturnsCopy_PackageStateUnchanged pins that the public
// Categories() helper hands back a fresh map. A regression that
// returned the package-internal map directly would let one caller
// mutate the state another caller depends on.
func TestCategoriesReturnsCopy_PackageStateUnchanged(t *testing.T) {
	t.Parallel()
	first := Categories()
	first["aws_sqs_queue"] = "DELETED-BY-TEST"
	delete(first, "aws_vpc")

	second := Categories()
	if got := second["aws_sqs_queue"]; got != CategoryEvents {
		t.Errorf("second call sees mutation from first: aws_sqs_queue=%q want %q", got, CategoryEvents)
	}
	if _, ok := second["aws_vpc"]; !ok {
		t.Errorf("second call missing aws_vpc deleted from first; package map is shared")
	}
	// Defensive: Category(...) reads through the package map; mutating
	// the returned copy must not affect it either.
	if got := Category("aws_sqs_queue"); got != CategoryEvents {
		t.Errorf("Category(aws_sqs_queue)=%q, want %q after caller mutated copy", got, CategoryEvents)
	}
}

// snapshotCategoryMap renders the category map as deterministic
// tab-separated text suitable for diffing against a golden file.
// Sorted by Terraform type so the output is byte-identical across
// runs and across machines.
func snapshotCategoryMap() string {
	keys := make([]string, 0, len(categoryByTFType))
	for k := range categoryByTFType {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('\t')
		b.WriteString(categoryByTFType[k])
		b.WriteByte('\n')
	}
	return b.String()
}

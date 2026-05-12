package policy

import (
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// syntheticTypePrefix is the namespace used by registry_test.go for
// synthetic test-only tfTypes. Production tests in this package must
// filter out registrations carrying this prefix so concurrent test
// runs don't observe each other through RegisteredTypes() or LintAll().
const syntheticTypePrefix = "policy_test_"

// phase1Types pins the exact set of import resource types that must
// have a Layer 2 policy registered. Adding or removing a type requires
// updating this list — the diff makes the surface change explicit. The
// name "phase1" predates Bundle 9 (#385) which expanded GCP coverage
// from 5 to 25 types; rename to coveredTypes is a future follow-up.
var phase1Types = []string{
	"aws_cloudwatch_log_group",
	"aws_dynamodb_table",
	"aws_lambda_function",
	"aws_secretsmanager_secret",
	"aws_sqs_queue",
	"google_cloud_run_v2_service",
	"google_cloudfunctions2_function",
	"google_compute_address",
	"google_compute_firewall",
	"google_compute_forwarding_rule",
	"google_compute_global_address",
	"google_compute_global_forwarding_rule",
	"google_compute_instance",
	"google_compute_network",
	"google_compute_router",
	"google_compute_target_https_proxy",
	"google_compute_url_map",
	"google_container_cluster",
	"google_container_node_pool",
	"google_kms_crypto_key",
	"google_kms_key_ring",
	"google_monitoring_alert_policy",
	"google_monitoring_dashboard",
	"google_monitoring_notification_channel",
	"google_pubsub_subscription",
	"google_pubsub_topic",
	"google_secret_manager_secret",
	"google_service_account",
	"google_sql_database_instance",
	"google_storage_bucket",
}

func TestPhase1Coverage(t *testing.T) {
	t.Parallel()
	for _, tfType := range phase1Types {
		t.Run(tfType, func(t *testing.T) {
			t.Parallel()
			m, ok := Lookup(tfType)
			require.True(t, ok, "no policy registered for %q", tfType)
			require.NotEmpty(t, m, "policy for %q is empty", tfType)
		})
	}
}

// TestRegisteredTypes_PhaseSetExact filters out synthetic test
// registrations (the "policy_test_" prefix used by registry_test.go
// and lint_test.go helpers) and asserts the remaining production
// registrations match the Phase 1 set exactly. Adding or removing a
// production tfType requires a deliberate edit to phase1Types.
func TestRegisteredTypes_PhaseSetExact(t *testing.T) {
	t.Parallel()
	got := RegisteredTypes()
	production := got[:0:0]
	for _, tfType := range got {
		if !strings.HasPrefix(tfType, syntheticTypePrefix) {
			production = append(production, tfType)
		}
	}
	want := append([]string(nil), phase1Types...)
	sort.Strings(want)
	assert.Equal(t, want, production,
		"production policy registrations must equal phase1Types exactly")
}

// TestPolicyRegistry_CoversGeneratedRegistry pins the invariant that
// every type registered in the Layer 1 typed-Attrs `generated` registry
// also has a Layer 2 policy registered. The two registries are
// independently populated (via WantedGoogle/WantedAWS driving codegen
// vs. hand-authored *.policy.go files), so a curator adding to
// WantedGoogle but forgetting the policy file would silently leave the
// new type with no axes — the wizard / Riley would fall back to default
// behavior, defeating the bundle's purpose.
//
// Symmetric to TestRegisteredTypes_PhaseSetExact, which guards the
// other direction (no orphan policies without a generated struct).
func TestPolicyRegistry_CoversGeneratedRegistry(t *testing.T) {
	t.Parallel()
	gen := generated.RegisteredTypes()
	pol := RegisteredTypes()
	production := pol[:0:0]
	for _, tfType := range pol {
		if !strings.HasPrefix(tfType, syntheticTypePrefix) {
			production = append(production, tfType)
		}
	}
	sort.Strings(gen)
	sort.Strings(production)
	assert.Equal(t, gen, production,
		"every generated.RegisteredTypes() entry must have a Layer 2 policy "+
			"registered (and vice versa). If you added a type to WantedGoogle "+
			"or WantedAWS, also author a corresponding *.policy.go file and "+
			"extend phase1Types.")
}

// TestGoogleComputeInstance_TagsIntentionallyUncurated pins the
// deliberate gap documented in google_compute_instance.policy.go:
// GCE network tags are NOT labels (they drive firewall source_tags /
// target_tags) but lint.go's tagAttrSuffixes hardcodes "tags" as
// label-shaped. Curating "tags": tagPolicy() would silently hide
// operator-meaningful network selectors; curating with any non-
// SystemOnly Edit trips CodeTagFieldNotSystemOnly. Until lint.go can
// exempt this case, the attr stays uncurated.
//
// This test fires if a well-meaning curator adds the entry back.
func TestGoogleComputeInstance_TagsIntentionallyUncurated(t *testing.T) {
	t.Parallel()
	m, ok := Lookup("google_compute_instance")
	require.True(t, ok, "google_compute_instance policy must be registered")
	_, present := m["tags"]
	assert.False(t, present,
		"google_compute_instance.tags must remain uncurated — see policy "+
			"file header comment for the lint.go::tagAttrSuffixes follow-up.")
}

func TestLintAll_Clean(t *testing.T) {
	t.Parallel()
	for _, tfType := range phase1Types {
		t.Run(tfType, func(t *testing.T) {
			t.Parallel()
			issues := Lint(tfType)
			if len(issues) == 0 {
				return
			}
			lines := make([]string, 0, len(issues))
			for _, i := range issues {
				lines = append(lines, i.String())
			}
			t.Fatalf("policy for %q lints with %d issue(s):\n%s",
				tfType, len(issues), strings.Join(lines, "\n"))
		})
	}
}

// TestKnownPathsNoShrink locks the curated Layer 2 surface against
// silent erosion. Every PR that adds, removes, or renames a policy
// path must also bump testdata/known_paths.golden so the diff is
// explicit. Set UPDATE_GOLDEN=1 to seed.
func TestKnownPathsNoShrink(t *testing.T) {
	t.Parallel()

	goldenPath := filepath.Join("testdata", "known_paths.golden")
	current := snapshot()

	if os.Getenv("UPDATE_GOLDEN") == "1" {
		require.NoError(t, os.MkdirAll(filepath.Dir(goldenPath), 0o755))
		require.NoError(t, os.WriteFile(goldenPath, []byte(current), 0o644))
		t.Logf("wrote golden: %s", goldenPath)
		return
	}

	want, err := os.ReadFile(goldenPath)
	require.NoError(t, err,
		"golden missing — run `UPDATE_GOLDEN=1 go test ./pkg/composer/imported/policy/ -run TestKnownPathsNoShrink`")
	require.Equal(t, string(want), current,
		"policy surface drifted from %s. If this is intentional, re-seed via UPDATE_GOLDEN=1.",
		goldenPath)
}

// snapshot emits a sorted, stable text representation of the entire
// curated policy surface for diffing purposes. Format:
//
//	<tfType>\t<path>\t<Role>\t<Pillar>\t<Visibility>\t<Edit>\t<Sensitivity>\t<ChangeRisk>\n
//
// Rationale is intentionally NOT included — it is freeform prose that
// would dirty the diff for non-surface changes.
func snapshot() string {
	tfTypes := RegisteredTypes()
	var b strings.Builder
	for _, t := range tfTypes {
		// Skip synthetic types that may have leaked from earlier tests.
		if !isPhase1(t) {
			continue
		}
		m, _ := Lookup(t)
		paths := make([]string, 0, len(m))
		for p := range m {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		for _, p := range paths {
			fp := m[p]
			b.WriteString(t)
			b.WriteByte('\t')
			b.WriteString(p)
			b.WriteByte('\t')
			b.WriteString(string(fp.Role))
			b.WriteByte('\t')
			b.WriteString(string(fp.Pillar))
			b.WriteByte('\t')
			b.WriteString(string(fp.Visibility))
			b.WriteByte('\t')
			b.WriteString(string(fp.Edit))
			b.WriteByte('\t')
			b.WriteString(string(fp.Sensitivity))
			b.WriteByte('\t')
			b.WriteString(string(fp.ChangeRisk))
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func isPhase1(tfType string) bool {
	return slices.Contains(phase1Types, tfType)
}

package policy

import (
	"os"
	"path/filepath"
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

// coveredTypes returns the set of production tfTypes that must have a
// Layer 2 policy registered. Derived directly from the generated Layer 1
// registry so adding a type to WantedAWS / WantedGoogle automatically
// flows here — there is no parallel hand-maintained list to keep in
// sync. The invariant that every generated type has a policy (and vice
// versa) is enforced by TestPolicyRegistry_CoversGeneratedRegistry
// below; this helper just hands the same set to the range-based tests.
func coveredTypes() []string {
	return generated.RegisteredTypes()
}

func TestCoveredTypesHavePolicies(t *testing.T) {
	t.Parallel()
	for _, tfType := range coveredTypes() {
		t.Run(tfType, func(t *testing.T) {
			t.Parallel()
			m, ok := Lookup(tfType)
			require.True(t, ok, "no policy registered for %q", tfType)
			require.NotEmpty(t, m, "policy for %q is empty", tfType)
		})
	}
}

// TestPolicyRegistry_CoversGeneratedRegistry pins the invariant that
// every type registered in the Layer 1 typed-Attrs `generated` registry
// also has a Layer 2 policy registered. The two registries are
// independently populated (via WantedGoogle/WantedAWS driving codegen
// vs. hand-authored *.policy.go files), so a curator adding to
// WantedGoogle but forgetting the policy file would silently leave the
// new type with no axes — the wizard / interactive agent would fall back to default
// behavior, defeating the bundle's purpose.
//
// This is the single source of truth for the covered-set invariant —
// coveredTypes() above derives from generated.RegisteredTypes(), so the
// older "exact match against a hand-maintained list" test would just be
// a tautology.
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
			"or WantedAWS, also author a corresponding *.policy.go file.")
}

// TestTagsIntentionallyUncurated pins the deliberate gap documented in
// google_compute_instance.policy.go and google_cloudbuild_trigger.policy.go.
// Neither type's `tags` attribute is a label:
//
//   - compute_instance.tags drives firewall source_tags / target_tags
//     (operationally meaningful network selectors)
//   - cloudbuild_trigger.tags is a free-text set of operator annotations
//
// But lint.go's `tagAttrSuffixes` hardcodes `"tags"` as label-shaped, so
// any non-SystemOnly curation trips CodeTagFieldNotSystemOnly while
// tagPolicy() (SystemOnly+Hidden+Redacted) is semantically wrong for both.
// Until the lint exemption lands, the attrs stay uncurated.
//
// This test fires if a well-meaning curator adds the entry back.
func TestTagsIntentionallyUncurated(t *testing.T) {
	t.Parallel()
	cases := []string{
		"google_compute_instance",
		"google_cloudbuild_trigger",
	}
	for _, tfType := range cases {
		t.Run(tfType, func(t *testing.T) {
			t.Parallel()
			m, ok := Lookup(tfType)
			require.True(t, ok, "%s policy must be registered", tfType)
			_, present := m["tags"]
			assert.False(t, present,
				"%s.tags must remain uncurated — see policy file header for the lint.go::tagAttrSuffixes follow-up", tfType)
		})
	}
}

func TestLintAll_Clean(t *testing.T) {
	t.Parallel()
	for _, tfType := range coveredTypes() {
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
		if !isCovered(t) {
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

func isCovered(tfType string) bool {
	_, _, ok := generated.Lookup(tfType)
	return ok
}

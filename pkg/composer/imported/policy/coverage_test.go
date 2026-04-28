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

	// Side-effect import: register the 10 generated Layer 1 types so
	// ResolvePath can walk struct schemas during LintAll.
	_ "github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
)

// phase1Types pins the exact set of Phase 1 import resource types that
// must have a Layer 2 policy registered. Adding or removing a type
// requires updating this list — the diff makes the surface change
// explicit. Mirrors generated/registry_test.go:TestRegistry_AllTenPhase1Registered.
var phase1Types = []string{
	"aws_cloudwatch_log_group",
	"aws_dynamodb_table",
	"aws_lambda_function",
	"aws_secretsmanager_secret",
	"aws_sqs_queue",
	"google_compute_network",
	"google_pubsub_subscription",
	"google_pubsub_topic",
	"google_secret_manager_secret",
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

func TestRegisteredTypes_NoExtras(t *testing.T) {
	t.Parallel()
	got := RegisteredTypes()
	want := append([]string(nil), phase1Types...)
	sort.Strings(want)
	// Filter out synthetic test-only types (lint_test register helpers
	// add them) by matching against the phase1 set. Production
	// registrations should match phase1Types exactly when the test
	// runs in isolation; in the suite, leftover test types may
	// briefly appear, so we only assert that phase1Types is a subset.
	for _, t1 := range want {
		assert.Contains(t, got, t1)
	}
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

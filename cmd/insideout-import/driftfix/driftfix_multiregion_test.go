package driftfix

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// mkPlannableStack writes the providers.tf + generated.tf pair that marks a
// directory as a plannable genconfig stack (the signal driftfix uses to
// detect single- vs multi-region layouts).
func mkPlannableStack(t *testing.T, dir, generatedBody string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "providers.tf"), []byte("provider \"aws\" {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, generatedFile), []byte(generatedBody), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestPlannableStacks_Detection pins the single- vs multi-region detection:
// a Workdir that is itself a stack is single; a parent with only the debug
// concat plus region subdirs is multi; an empty/unrecognized dir falls back to
// the Workdir so legacy/fake-runner behavior is unchanged.
func TestPlannableStacks_Detection(t *testing.T) {
	t.Parallel()

	t.Run("single-region: Workdir is the stack", func(t *testing.T) {
		wd := t.TempDir()
		mkPlannableStack(t, wd, "# body\n")
		got := plannableStacks(wd)
		if len(got) != 1 || got[0] != wd {
			t.Fatalf("plannableStacks = %v, want [%s]", got, wd)
		}
	})

	t.Run("multi-region: descend into region subdirs", func(t *testing.T) {
		wd := t.TempDir()
		// Parent carries ONLY the debug concat (no providers.tf) — not plannable.
		if err := os.WriteFile(filepath.Join(wd, generatedFile), []byte("# concat\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		// Subdirs in sorted order so the expectation matches the sorted return.
		want := []string{
			filepath.Join(wd, "region-eu_west_1"),
			filepath.Join(wd, "region-us_east_1"),
		}
		for _, d := range want {
			mkPlannableStack(t, d, "# region body\n")
		}
		got := plannableStacks(wd)
		if !slices.Equal(got, want) {
			t.Fatalf("plannableStacks = %v, want %v", got, want)
		}
	})

	t.Run("fallback: nothing plannable returns Workdir", func(t *testing.T) {
		wd := t.TempDir()
		got := plannableStacks(wd)
		if len(got) != 1 || got[0] != wd {
			t.Fatalf("plannableStacks = %v, want [%s]", got, wd)
		}
	})
}

// TestRun_MultiRegion_ConvergesEachSubdirAndRemerges proves the multi-region
// path: every region subdir is drift-fixed via its own runner, and the parent
// generated.tf is re-merged from the per-region files (overwriting the stale
// concat) so dep-chase's text-read of the parent reflects the converged stacks.
func TestRun_MultiRegion_ConvergesEachSubdirAndRemerges(t *testing.T) {
	t.Parallel()
	wd := t.TempDir()

	bodies := map[string]string{
		"region-eu_west_1": "# eu\nresource \"aws_vpc\" \"eu\" {}\n",
		"region-us_east_1": "# ue1\nresource \"aws_vpc\" \"ue1\" {}\n",
	}
	for alias, body := range bodies {
		mkPlannableStack(t, filepath.Join(wd, alias), body)
	}
	// Stale parent concat that the re-merge must overwrite.
	if err := os.WriteFile(filepath.Join(wd, generatedFile), []byte("# STALE\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// One empty scripted runner per region: empty plan → converge on iter 1.
	runners := map[string]*scriptedRunner{
		"region-eu_west_1": {},
		"region-us_east_1": {},
	}
	opts := Options{
		Workdir: wd,
		newRunner: func(stackDir string, _ io.Writer) (terraformRunner, error) {
			r, ok := runners[filepath.Base(stackDir)]
			if !ok {
				t.Errorf("unexpected stack dir %q", stackDir)
				return &scriptedRunner{}, nil
			}
			return r, nil
		},
	}

	res, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Each region's runner was actually exercised.
	for alias, r := range runners {
		if r.planCalls == 0 {
			t.Errorf("region %s: PlanTo never called", alias)
		}
	}

	// Parent generated.tf was re-merged from the subdirs (stale concat gone,
	// both region bodies present).
	merged, err := os.ReadFile(filepath.Join(wd, generatedFile))
	if err != nil {
		t.Fatal(err)
	}
	got := string(merged)
	if strings.Contains(got, "STALE") {
		t.Errorf("parent generated.tf still stale:\n%s", got)
	}
	for _, want := range []string{`"aws_vpc" "eu"`, `"aws_vpc" "ue1"`} {
		if !strings.Contains(got, want) {
			t.Errorf("parent generated.tf missing %q:\n%s", want, got)
		}
	}
	if res.GeneratedPath != filepath.Join(wd, generatedFile) {
		t.Errorf("Result.GeneratedPath = %q, want parent generated.tf", res.GeneratedPath)
	}
}

// TestRun_MultiRegion_PropagatesRegionError proves a failure in any single
// region aborts the run with a region-labeled error (errgroup fail-fast).
func TestRun_MultiRegion_PropagatesRegionError(t *testing.T) {
	t.Parallel()
	wd := t.TempDir()
	for _, alias := range []string{"region-a", "region-b"} {
		mkPlannableStack(t, filepath.Join(wd, alias), "# body\n")
	}
	runners := map[string]*scriptedRunner{
		"region-a": {planErr: errors.New("boom")},
		"region-b": {},
	}
	opts := Options{
		Workdir: wd,
		newRunner: func(stackDir string, _ io.Writer) (terraformRunner, error) {
			return runners[filepath.Base(stackDir)], nil
		},
	}
	_, err := Run(context.Background(), opts)
	if err == nil {
		t.Fatal("expected error from failing region, got nil")
	}
	if !strings.Contains(err.Error(), "region-a") {
		t.Errorf("error should name the failing region, got: %v", err)
	}
}

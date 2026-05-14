package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEmitPolicyTS_GoldenFiles is the byte-for-byte parity gate for the
// policy-ts emitter. It runs the subcommand into a tempdir and diffs
// the emitted google_storage_bucket / aws_sqs_queue / _policy modules
// against committed goldens in testdata/policy_ts/.
//
// To refresh after a deliberate policy / template change:
//
//	go run ./cmd/imported-codegen policy-ts --out /tmp/policy-out
//	cp /tmp/policy-out/{google_storage_bucket.policy.ts,aws_sqs_queue.policy.ts,_policy.ts} \
//	    cmd/imported-codegen/testdata/policy_ts/
//
// The pair is chosen for cross-axis coverage: storage_bucket exercises
// every Pillar value plus AlwaysReplace / MayReplace ChangeRisk;
// sqs_queue covers the JSON-projected subpath shape
// (redrive_policy.deadLetterTargetArn). The shared _policy.ts is in
// the golden set so a refactor of the projection runtime is caught at
// review time, not silently emitted.
func TestEmitPolicyTS_GoldenFiles(t *testing.T) {
	t.Parallel()
	outDir := t.TempDir()

	rc := runPolicyTS([]string{
		"--out", outDir,
		"--types", "google_storage_bucket,aws_sqs_queue",
	})
	require.Equal(t, 0, rc, "runPolicyTS must exit 0")

	for _, name := range []string{
		"google_storage_bucket.policy.ts",
		"aws_sqs_queue.policy.ts",
		"_policy.ts",
	} {
		got, err := os.ReadFile(filepath.Join(outDir, name))
		require.NoErrorf(t, err, "missing emitted file %s", name)
		want, err := os.ReadFile(filepath.Join("testdata", "policy_ts", name))
		require.NoErrorf(t, err, "missing golden file %s — refresh via the doc comment above", name)
		if !bytes.Equal(got, want) {
			t.Errorf("%s: emitted output drifts from golden — refresh testdata/policy_ts/%s or fix the regression", name, name)
			// Surface a short diff hint for the reviewer. Full diff is in
			// `git diff` if the developer refreshes the golden.
			for i, line := range strings.SplitN(string(got), "\n", 80) {
				if i >= len(strings.SplitN(string(want), "\n", 80)) {
					break
				}
				wantLines := strings.SplitN(string(want), "\n", 80)
				if line != wantLines[i] {
					t.Logf("  first divergence at line %d:\n    got:  %s\n    want: %s", i+1, line, wantLines[i])
					break
				}
			}
		}
	}
}

// TestEmitPolicyTS_AllRegistered runs the subcommand for the full
// registered set and asserts the expected file count (one .policy.ts
// per registered type + _policy.ts + _policy_registry.ts) plus that
// every file carries the @generated sentinel and a POLICY constant.
func TestEmitPolicyTS_AllRegistered(t *testing.T) {
	t.Parallel()
	outDir := t.TempDir()

	rc := runPolicyTS([]string{"--out", outDir})
	require.Equal(t, 0, rc)

	registered := policy.RegisteredTypes()
	for _, tfType := range registered {
		path := filepath.Join(outDir, tfType+".policy.ts")
		b, err := os.ReadFile(path)
		require.NoErrorf(t, err, "missing emitted file for %s", tfType)
		content := string(b)
		for _, sentinel := range []string{
			"// @generated",
			"export const POLICY",
			"export function visibleFields(",
			"export function editableFields(",
			`from "./_policy";`,
		} {
			assert.Containsf(t, content, sentinel, "%s.policy.ts missing sentinel %q", tfType, sentinel)
		}
	}

	value, err := os.ReadFile(filepath.Join(outDir, "_policy.ts"))
	require.NoError(t, err)
	assert.Contains(t, string(value), "export type FieldRole =")
	assert.Contains(t, string(value), "export function projectFields(")

	registry, err := os.ReadFile(filepath.Join(outDir, "_policy_registry.ts"))
	require.NoError(t, err)
	for _, tfType := range registered {
		assert.Containsf(t, string(registry), fmt.Sprintf(`"%s":`, tfType), "_policy_registry.ts missing entry for %s", tfType)
	}
}

// TestEmitPolicyTS_UnknownTypesRejected pins the typo-rejection
// behavior on the --types flag: an unknown type causes runPolicyTS to
// exit non-zero rather than silently emit a partial registry. Mirrors
// the same gate on runZod.
func TestEmitPolicyTS_UnknownTypesRejected(t *testing.T) {
	t.Parallel()
	outDir := t.TempDir()
	rc := runPolicyTS([]string{
		"--out", outDir,
		"--types", "google_storage_bucket,not_a_real_type",
	})
	assert.NotEqual(t, 0, rc, "unknown --types entry must surface as non-zero exit")
}

// TestEmitPolicyTS_RowParityWithGoProjection is the cross-language
// parity gate: emit POLICY rows, parse them back from the TS literal
// (best-effort regex extraction), then assert each emitted row has the
// same axis values as the Go-side policy.Map entry for the same path.
//
// This is the structural twin of TestEmitZodMetadataMatchesGoSchema —
// the byte-for-byte template-literal shape is brittle to refactor, but
// the *axis values per path* must match Go or the generated TS lies
// about visibility / edit / sensitivity.
//
// We avoid spawning `npx tsx` (heavy, flaky in CI) by parsing the
// emitted literal in Go. The shape is constrained by the template, so
// the regex is tight; if the template changes shape, this test breaks
// loudly and you re-derive the regex once.
func TestEmitPolicyTS_RowParityWithGoProjection(t *testing.T) {
	t.Parallel()
	outDir := t.TempDir()

	rc := runPolicyTS([]string{"--out", outDir})
	require.Equal(t, 0, rc)

	for _, tfType := range policy.RegisteredTypes() {
		t.Run(tfType, func(t *testing.T) {
			t.Parallel()
			polMap, ok := policy.Lookup(tfType)
			require.True(t, ok)

			b, err := os.ReadFile(filepath.Join(outDir, tfType+".policy.ts"))
			require.NoError(t, err)
			rows := parsePolicyRows(t, string(b))

			require.Equalf(t, len(polMap), len(rows),
				"%s: row count drift (go=%d, ts=%d)", tfType, len(polMap), len(rows))

			for _, row := range rows {
				fp, ok := polMap[row.path]
				require.Truef(t, ok, "%s: emitted POLICY row %q has no Go-side policy.Map entry", tfType, row.path)

				assert.Equalf(t, string(fp.Role), row.role,
					"%s.%s: role drift", tfType, row.path)
				assert.Equalf(t, pillarToWire(fp.Pillar), row.pillar,
					"%s.%s: pillar drift", tfType, row.path)
				assert.Equalf(t, string(fp.Visibility), row.visibility,
					"%s.%s: visibility drift", tfType, row.path)
				assert.Equalf(t, string(fp.Edit), row.edit,
					"%s.%s: edit drift", tfType, row.path)
				assert.Equalf(t, sensitivityToWire(fp.Sensitivity), row.sensitivity,
					"%s.%s: sensitivity drift", tfType, row.path)
				assert.Equalf(t, changeRiskToWire(fp.ChangeRisk), row.changeRisk,
					"%s.%s: changeRisk drift", tfType, row.path)
			}
		})
	}
}

// tsPolicyRow is the per-row shape parsed back out of an emitted
// POLICY array. Field names match the JSON keys the template emits.
type tsPolicyRow struct {
	path        string
	role        string
	pillar      string
	visibility  string
	edit        string
	sensitivity string
	changeRisk  string
}

// parsePolicyRows extracts every `{ path: "...", ... }` object literal
// out of the emitted POLICY array. Tolerant to whitespace and trailing
// commas; relies on the template emitting each row on a single line
// (which it does — see policy.ts.tmpl).
func parsePolicyRows(t *testing.T, content string) []tsPolicyRow {
	t.Helper()
	// Find the POLICY array bounds. Match on the opening `[` after
	// `POLICY: ReadonlyArray<FieldRow> = [` to skip past comment blocks.
	startMarker := "export const POLICY: ReadonlyArray<FieldRow> = ["
	start := strings.Index(content, startMarker)
	require.NotEqualf(t, -1, start, "POLICY array literal not found")
	start += len(startMarker)
	end := strings.Index(content[start:], "];")
	require.NotEqualf(t, -1, end, "POLICY array close `];` not found")
	body := content[start : start+end]

	var rows []tsPolicyRow
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		row := tsPolicyRow{
			path:        extractField(t, line, "path"),
			role:        extractField(t, line, "role"),
			pillar:      extractField(t, line, "pillar"),
			visibility:  extractField(t, line, "visibility"),
			edit:        extractField(t, line, "edit"),
			sensitivity: extractField(t, line, "sensitivity"),
			changeRisk:  extractField(t, line, "changeRisk"),
		}
		rows = append(rows, row)
	}
	return rows
}

// extractField pulls the quoted value of `key: "value"` out of a
// single-line object literal. Returns "" if the key isn't present —
// the caller treats absence as a test failure via the equality
// assertion (the field is always emitted by the template).
func extractField(t *testing.T, line, key string) string {
	t.Helper()
	needle := key + `: "`
	i := strings.Index(line, needle)
	if i == -1 {
		return ""
	}
	rest := line[i+len(needle):]
	j := strings.Index(rest, `"`)
	if j == -1 {
		return ""
	}
	return rest[:j]
}

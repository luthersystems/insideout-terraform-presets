package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// repoRoot returns the repo root from the cmd/imported-codegen test
// working directory. Tests run from the package dir; the schemas live
// two levels up.
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	return filepath.Join(wd, "..", "..")
}

// TestEmitZod_AllWantedTypes is the smoke test: runs the zod
// subcommand against the committed filtered schemas into a tempdir
// and asserts the expected file set is present, each .ts file
// contains the expected exported symbols, and the shared _value.ts
// and _registry.ts are well-formed.
func TestEmitZod_AllWantedTypes(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	outDir := t.TempDir()

	rc := runZod([]string{
		"--aws-schema", filepath.Join(root, "schemas", "aws.filtered.json"),
		"--google-schema", filepath.Join(root, "schemas", "google.filtered.json"),
		"--google-beta-schema", filepath.Join(root, "schemas", "google-beta.filtered.json"),
		"--out", outDir,
	})
	require.Equal(t, 0, rc, "runZod must exit 0")

	want := append([]string{}, WantedAWS...)
	want = append(want, WantedGoogle...)
	want = append(want, WantedGoogleBeta...)

	for _, tfType := range want {
		path := filepath.Join(outDir, tfType+".ts")
		b, err := os.ReadFile(path)
		require.NoErrorf(t, err, "missing emitted file for %s", tfType)
		content := string(b)
		goName := GoName(tfType)
		for _, sentinel := range []string{
			"// @generated",
			`import { z } from "zod";`,
			`import { expressionAware, type FieldMeta } from "./_value";`,
			"export const Z" + goName,
			"export const " + goName + "Schema",
			"export const " + goName + "ProviderSource",
			"as const satisfies Record<string, FieldMeta>;",
		} {
			assert.Containsf(t, content, sentinel, "%s.ts missing sentinel %q", tfType, sentinel)
		}
	}

	value, err := os.ReadFile(filepath.Join(outDir, "_value.ts"))
	require.NoError(t, err)
	assert.Contains(t, string(value), "export const expressionAware =")
	assert.Contains(t, string(value), "export type FieldMeta =")

	registry, err := os.ReadFile(filepath.Join(outDir, "_registry.ts"))
	require.NoError(t, err)
	for _, tfType := range want {
		assert.Containsf(t, string(registry), fmt.Sprintf(`"%s":`, tfType), "_registry.ts missing entry for %s", tfType)
	}
}

// TestEmitZod_FilterFlag pins the --types subset behavior: only the
// listed types emit a .ts file; the shared files always emit; the
// registry only references the listed types.
func TestEmitZod_FilterFlag(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	outDir := t.TempDir()

	rc := runZod([]string{
		"--aws-schema", filepath.Join(root, "schemas", "aws.filtered.json"),
		"--google-schema", filepath.Join(root, "schemas", "google.filtered.json"),
		"--google-beta-schema", filepath.Join(root, "schemas", "google-beta.filtered.json"),
		"--out", outDir,
		"--types", "aws_sqs_queue,google_storage_bucket",
	})
	require.Equal(t, 0, rc)

	entries, err := os.ReadDir(outDir)
	require.NoError(t, err)
	got := make(map[string]bool, len(entries))
	for _, e := range entries {
		got[e.Name()] = true
	}
	want := map[string]bool{
		"_value.ts":               true,
		"_registry.ts":            true,
		"aws_sqs_queue.ts":        true,
		"google_storage_bucket.ts": true,
	}
	assert.Equal(t, want, got, "filtered emission should contain exactly the listed types + shared files")
}

// TestEmitZodMetadataMatchesGoSchema is the load-bearing byte-for-byte
// parity gate from issue #400. For every Wanted type that has a
// registered Go schema, the emitted TS metadata constant must carry
// the same Required / Optional / Computed / Sensitive / Replacement
// values per field as generated.<Type>Schema.
//
// The test does not parse the .ts as JSON (TS object literals use
// unquoted keys); instead, for each field it asserts the expected
// `key: true,` substrings appear on the field's line, and the
// reverse: any flag NOT set on the Go side must NOT appear on the
// emitted line.
func TestEmitZodMetadataMatchesGoSchema(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	outDir := t.TempDir()

	rc := runZod([]string{
		"--aws-schema", filepath.Join(root, "schemas", "aws.filtered.json"),
		"--google-schema", filepath.Join(root, "schemas", "google.filtered.json"),
		"--google-beta-schema", filepath.Join(root, "schemas", "google-beta.filtered.json"),
		"--out", outDir,
	})
	require.Equal(t, 0, rc)

	registered := map[string]bool{}
	for _, t := range generated.RegisteredTypes() {
		registered[t] = true
	}

	want := append([]string{}, WantedAWS...)
	want = append(want, WantedGoogle...)
	want = append(want, WantedGoogleBeta...)

	for _, tfType := range want {
		if !registered[tfType] {
			// Nested-block fields appear in the schema map with no
			// Required/Optional bits set if the Go side didn't emit
			// one — skip types missing on the Go side rather than
			// breaking parity over a deletion the codegen hasn't
			// reflected yet.
			continue
		}
		_, schema, ok := generated.Lookup(tfType)
		require.Truef(t, ok, "generated.Lookup(%q) should succeed for registered type", tfType)

		path := filepath.Join(outDir, tfType+".ts")
		b, err := os.ReadFile(path)
		require.NoError(t, err)
		content := string(b)

		// Sort fields for stable failure messages.
		fields := make([]string, 0, len(schema))
		for k := range schema {
			fields = append(fields, k)
		}
		sort.Strings(fields)

		for _, field := range fields {
			fs := schema[field]
			// Locate the line for this field. Tolerant to surrounding
			// whitespace; field name appears once in the metadata map.
			needle := fmt.Sprintf(`"%s": { `, field)
			idx := strings.Index(content, needle)
			require.NotEqualf(t, -1, idx, "missing metadata entry %q in %s.ts", field, tfType)
			eol := strings.Index(content[idx:], "\n")
			require.NotEqual(t, -1, eol)
			line := content[idx : idx+eol]

			for flagName, want := range map[string]bool{
				"required: true,":  fs.Required,
				"optional: true,":  fs.Optional,
				"computed: true,":  fs.Computed,
				"sensitive: true,": fs.Sensitive,
			} {
				if want {
					assert.Containsf(t, line, flagName, "%s.%s: expected %q in TS", tfType, field, flagName)
				} else {
					assert.NotContainsf(t, line, flagName, "%s.%s: unexpected %q in TS", tfType, field, flagName)
				}
			}

			wireRepl := string(fs.Replacement)
			if wireRepl == "" {
				wireRepl = "unknown"
			}
			expected := fmt.Sprintf(`replacement: "%s"`, wireRepl)
			assert.Containsf(t, line, expected, "%s.%s: replacement wire mismatch", tfType, field)
		}
	}
}

package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"text/template"

	tfjson "github.com/hashicorp/terraform-json"
	"github.com/luthersystems/insideout-terraform-presets/pkg/composer/imported/generated"
	"github.com/luthersystems/insideout-terraform-presets/pkg/imported/forcenew"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zclconf/go-cty/cty"
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
		"--providers-tf", filepath.Join(root, "schemas", "providers.tf"),
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

	// Spot-check the Z<Name> object body for a real type. Sentinel-only
	// checks above would still pass if buildZodTypeData emitted an
	// empty z.object({}) (or replaced every field with z.never()) —
	// asserting one known field expression catches that class of bug
	// without coupling the test to schema churn.
	sqs, err := os.ReadFile(filepath.Join(outDir, "aws_sqs_queue.ts"))
	require.NoError(t, err)
	assert.Contains(t, string(sqs), "name: expressionAware(z.string()).optional(),",
		"ZAWSSQSQueue body must contain the canonical `name` field expression")
	assert.Contains(t, string(sqs), "fifo_queue: expressionAware(z.boolean()).optional(),",
		"ZAWSSQSQueue body must wrap booleans via expressionAware")

	registry, err := os.ReadFile(filepath.Join(outDir, "_registry.ts"))
	require.NoError(t, err)
	for _, tfType := range want {
		assert.Containsf(t, string(registry), fmt.Sprintf(`"%s":`, tfType), "_registry.ts missing entry for %s", tfType)
	}

	// Versions map: load the same providers.tf the emitter consumed
	// and assert the emitted block is byte-anchored against the
	// expected source/version pairs in template-iteration order.
	// This is the load-bearing TS-side drift signal — a downstream
	// consumer reads versions[providerSource] to stamp
	// ResourceIdentity.providerVersion at import time. A weaker
	// Contains-per-pair check would pass if the pairs leaked into a
	// comment or a second (corrupt) map; the block-anchored check +
	// single-occurrence guard catches both regressions.
	pins, err := LoadProviderPins(filepath.Join(root, "schemas", "providers.tf"))
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(string(registry), "export const versions = {"),
		"_registry.ts must declare exactly one versions map")
	wantBlock := fmt.Sprintf(
		"export const versions = {\n"+
			"  %q: %q,\n"+
			"  %q: %q,\n"+
			"  %q: %q,\n"+
			"} as const;",
		AWSProviderSource, pins.AWS,
		GoogleProviderSource, pins.Google,
		GoogleBetaProviderSource, pins.GoogleBeta,
	)
	assert.Contains(t, string(registry), wantBlock,
		"_registry.ts versions block must match expected layout byte-for-byte")
}

// TestParseTypesFilter pins the parser semantics: empty / whitespace
// / commas-only collapses to "all"; a populated CSV becomes a
// set-based filter; surrounding whitespace per-token is trimmed.
func TestParseTypesFilter(t *testing.T) {
	t.Parallel()

	all := parseTypesFilter("")
	assert.True(t, all.all, "empty input means all-pass")
	assert.True(t, all.want("anything"))

	whitespace := parseTypesFilter("   ")
	assert.True(t, whitespace.all, "whitespace-only input means all-pass")

	commasOnly := parseTypesFilter(",,, ,")
	assert.False(t, commasOnly.all, "commas-with-no-tokens means empty set (not all)")
	assert.False(t, commasOnly.want("aws_sqs_queue"), "empty set matches nothing")

	csv := parseTypesFilter("aws_sqs_queue, google_storage_bucket ,google_pubsub_topic")
	assert.False(t, csv.all)
	assert.True(t, csv.want("aws_sqs_queue"))
	assert.True(t, csv.want("google_storage_bucket"), "surrounding whitespace must be trimmed per-token")
	assert.True(t, csv.want("google_pubsub_topic"))
	assert.False(t, csv.want("aws_lambda_function"))
}

// TestTypesFilter_UnknownAgainst pins the validation gate: --types
// entries not present in the union of Wanted* slices are returned
// (sorted) so runZod can reject the run with an actionable message
// instead of silently emitting a partial registry.
func TestTypesFilter_UnknownAgainst(t *testing.T) {
	t.Parallel()

	allFilter := parseTypesFilter("")
	assert.Nil(t, allFilter.unknownAgainst([]string{"aws_sqs_queue"}), "all-pass filter has no unknowns by definition")

	// Three unknowns inserted out of sorted order so deleting the
	// sort.Strings call in unknownAgainst surfaces as a deterministic
	// failure (Go map iteration would otherwise produce sorted order
	// only ~1/6 of the time, leading to CI flakes instead of clean
	// failures).
	mixed := parseTypesFilter("aws_sqs_queue,typo_zzz,typo_aaa,google_storage_bucket,typo_mmm")
	unknown := mixed.unknownAgainst(
		[]string{"aws_sqs_queue"},
		[]string{"google_storage_bucket"},
		[]string{},
	)
	assert.Equal(t, []string{"typo_aaa", "typo_mmm", "typo_zzz"}, unknown, "unknowns must be returned sorted")

	allKnown := parseTypesFilter("aws_sqs_queue,google_storage_bucket")
	assert.Empty(t, allKnown.unknownAgainst(
		[]string{"aws_sqs_queue"},
		[]string{"google_storage_bucket"},
	))
}

// TestBuildBlockNestedZod_NilBlock pins the schema-edge case: a
// nested block whose Block payload is nil emits a single empty
// ZodNestedType (matches the Go-side buildBlockNested behavior at
// emit.go:204).
func TestBuildBlockNestedZod_NilBlock(t *testing.T) {
	t.Parallel()
	nb := &tfjson.SchemaBlockType{Block: nil, NestingMode: tfjson.SchemaNestingModeList}
	got, err := buildBlockNestedZod("ParentChild", nb)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "ParentChild", got[0].GoName)
	assert.Empty(t, got[0].Fields, "nil-Block must produce an empty Fields slice")
}

// TestBuildBlockNestedZod_RecursiveBlocks pins the depth-first
// declaration order and zod-expression shape for nested blocks across
// the two nesting-mode branches (single → lazy, list/set → array of
// lazy).
func TestBuildBlockNestedZod_RecursiveBlocks(t *testing.T) {
	t.Parallel()
	leafSingle := &tfjson.SchemaBlockType{
		NestingMode: tfjson.SchemaNestingModeSingle,
		Block: &tfjson.SchemaBlock{
			Attributes: map[string]*tfjson.SchemaAttribute{
				"name": {AttributeType: cty.String, Required: true},
			},
		},
	}
	leafList := &tfjson.SchemaBlockType{
		NestingMode: tfjson.SchemaNestingModeList,
		Block: &tfjson.SchemaBlock{
			Attributes: map[string]*tfjson.SchemaAttribute{
				"port": {AttributeType: cty.Number, Optional: true},
			},
		},
	}
	parent := &tfjson.SchemaBlockType{
		NestingMode: tfjson.SchemaNestingModeList,
		Block: &tfjson.SchemaBlock{
			Attributes: map[string]*tfjson.SchemaAttribute{
				"enabled": {AttributeType: cty.Bool, Optional: true},
			},
			NestedBlocks: map[string]*tfjson.SchemaBlockType{
				// Map key order is non-deterministic; SortedBlockTypeNames
				// inside buildBlockNestedZod normalizes ("leaf" before "ports").
				"leaf":  leafSingle,
				"ports": leafList,
			},
		},
	}

	got, err := buildBlockNestedZod("FooParent", parent)
	require.NoError(t, err)
	require.Len(t, got, 3, "parent first, then children depth-first in sorted-name order")

	assert.Equal(t, "FooParent", got[0].GoName)
	require.Len(t, got[0].Fields, 3, "1 attr + 2 nested-block fields")
	assert.Equal(t, "enabled", got[0].Fields[0].TFName)
	assert.Equal(t, "expressionAware(z.boolean())", got[0].Fields[0].ZodType)
	// Single nesting mode → lazy reference, no array wrapper.
	assert.Equal(t, "leaf", got[0].Fields[1].TFName)
	assert.Equal(t, "z.lazy(() => ZFooParentLeaf)", got[0].Fields[1].ZodType)
	// List nesting mode → array of lazy reference.
	assert.Equal(t, "ports", got[0].Fields[2].TFName)
	assert.Equal(t, "z.array(z.lazy(() => ZFooParentPorts))", got[0].Fields[2].ZodType)

	assert.Equal(t, "FooParentLeaf", got[1].GoName)
	require.Len(t, got[1].Fields, 1)
	assert.Equal(t, "name", got[1].Fields[0].TFName)
	assert.Equal(t, "expressionAware(z.string())", got[1].Fields[0].ZodType)

	assert.Equal(t, "FooParentPorts", got[2].GoName)
	require.Len(t, got[2].Fields, 1)
	assert.Equal(t, "port", got[2].Fields[0].TFName)
	assert.Equal(t, "expressionAware(z.number())", got[2].Fields[0].ZodType)
}

// TestEmitZodTypeFile_TemplateHonorsReplacementWire renders the type
// template directly with hand-built ZodSchemaEntries to verify that
// the template emits the wire string verbatim from the
// ZodSchemaEntry.Replacement field. Catches a regression where someone
// hardcodes `replacement: "unknown"` in the template or in
// buildZodTypeData instead of plumbing the value through. Complements
// TestEmitZod_AlwaysReplaceForRegisteredForceNewFields below, which
// drives the full pipeline against the forcenew registry.
func TestEmitZodTypeFile_TemplateHonorsReplacementWire(t *testing.T) {
	t.Parallel()
	outDir := t.TempDir()

	tmpl, err := template.New("type.zod").Parse(zodTypeTemplateSrc)
	require.NoError(t, err)

	td := &ZodTypeData{
		TFType:         "synthetic_force_new",
		GoName:         "SyntheticForceNew",
		ProviderSource: "registry.terraform.io/hashicorp/synthetic",
		Fields: []ZodFieldData{
			{TFName: "name", ZodType: "expressionAware(z.string())"},
			{TFName: "size", ZodType: "expressionAware(z.number())"},
		},
		SchemaEntries: []ZodSchemaEntry{
			{TFName: "name", Required: true, Replacement: "always_replace"},
			{TFName: "size", Optional: true, Replacement: "never"},
		},
	}

	var buf bytes.Buffer
	require.NoError(t, tmpl.Execute(&buf, td))
	out := buf.String()

	require.NoError(t, os.WriteFile(filepath.Join(outDir, "synthetic.ts"), buf.Bytes(), 0o644))

	assert.Contains(t, out, `"name": { required: true, replacement: "always_replace", },`,
		"template must emit the wire string from SchemaEntry.Replacement, not a hardcoded constant")
	assert.Contains(t, out, `"size": { optional: true, replacement: "never", },`,
		"template must emit Never as never, not unknown")
}

// TestEmitZod_AlwaysReplaceForRegisteredForceNewFields is the
// end-to-end regression guard for issue #566. It exercises the full
// zod-emit pipeline against the committed filtered schemas and asserts
// that every field registered in pkg/imported/forcenew with
// ReplacementAlwaysReplace surfaces as `replacement: "always_replace"`
// in the emitted .ts.
//
// Why end-to-end and not unit: the prior failure mode silently fell
// through `buildZodTypeData`'s hardcoded "Unknown" because the
// terraform-json schema-attribute struct doesn't carry force_new — a
// unit test that hand-builds ZodSchemaEntry would have passed because
// it bypasses the lookup. This test drives `runZod` end-to-end so any
// regression that disconnects the forcenew overlay from the codegen
// pipeline fails loudly.
//
// The assertion iterates forcenew.RegisteredEntries() rather than
// hardcoding type/field pairs, so future overrides added to
// pkg/imported/forcenew/overrides.go automatically get end-to-end
// coverage without having to remember to extend this test.
func TestEmitZod_AlwaysReplaceForRegisteredForceNewFields(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	outDir := t.TempDir()

	rc := runZod([]string{
		"--aws-schema", filepath.Join(root, "schemas", "aws.filtered.json"),
		"--google-schema", filepath.Join(root, "schemas", "google.filtered.json"),
		"--google-beta-schema", filepath.Join(root, "schemas", "google-beta.filtered.json"),
		"--providers-tf", filepath.Join(root, "schemas", "providers.tf"),
		"--out", outDir,
	})
	require.Equal(t, 0, rc)

	// Require the seed isn't accidentally empty — a future PR that
	// drops every override from forcenew/overrides.go would otherwise
	// silently pass this whole test with zero iterations.
	entries := forcenew.RegisteredEntries()
	require.NotEmpty(t, entries,
		"forcenew registry is empty — every entry from overrides.go must be exercised here; if you intentionally cleared the registry, retire this test in the same PR")

	for _, e := range entries {
		if e.Behavior != generated.ReplacementAlwaysReplace {
			// Other behaviors (Never, MayReplace) are valid overrides
			// but not what this regression test guards. Skip rather
			// than fail so the test stays focused on the issue #566
			// failure mode.
			continue
		}
		path := filepath.Join(outDir, e.TFType+".ts")
		b, err := os.ReadFile(path)
		require.NoErrorf(t, err, "expected emitted %s.ts for forcenew entry", e.TFType)
		content := string(b)

		// Find the exact field line. The tolerant needle matches the
		// template's `"<name>": { ` prefix; whitespace inside the
		// braces can vary as the per-field flag set changes.
		needle := fmt.Sprintf(`"%s": { `, e.Field)
		idx := strings.Index(content, needle)
		require.NotEqualf(t, -1, idx,
			"forcenew entry %s.%s: field metadata line not found in %s.ts",
			e.TFType, e.Field, e.TFType)
		eol := strings.Index(content[idx:], "\n")
		require.NotEqual(t, -1, eol)
		line := content[idx : idx+eol]

		assert.Containsf(t, line, `replacement: "always_replace"`,
			"forcenew entry %s.%s should emit replacement=always_replace; got line: %s",
			e.TFType, e.Field, line)
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
		"--providers-tf", filepath.Join(root, "schemas", "providers.tf"),
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
		"_value.ts":                true,
		"_registry.ts":             true,
		"aws_sqs_queue.ts":         true,
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
		"--providers-tf", filepath.Join(root, "schemas", "providers.tf"),
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

	// Aggregate Wanted-vs-registered drift across all types so one
	// stale .gen.go shows up as a single actionable failure (with the
	// full list of missing types) instead of N independent skips that
	// hide the drift the test was added to catch.
	var unregistered []string
	for _, tfType := range want {
		if !registered[tfType] {
			unregistered = append(unregistered, tfType)
		}
	}
	require.Emptyf(t, unregistered,
		"Wanted* slices include types missing from generated.RegisteredTypes(): %v — run `make gen-imported` and commit the new .gen.go files",
		unregistered)

	for _, tfType := range want {
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
